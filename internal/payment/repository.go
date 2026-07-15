package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Payment struct {
	ID                string     `json:"payment_id"`
	JobID             string     `json:"job_id"`
	EmployerID        string     `json:"employer_id"`
	WorkerID          string     `json:"worker_id"`
	Amount            int64      `json:"amount"`
	PlatformFee       int64      `json:"platform_fee"`
	NetToWorker       int64      `json:"net_to_worker"`
	Status            string     `json:"status"`
	MidtransOrderID   string     `json:"midtrans_order_id"`
	MidtransSnapToken *string    `json:"snap_token,omitempty"`
	HeldAt            *time.Time `json:"held_at,omitempty"`
	ReleasedAt        *time.Time `json:"released_at,omitempty"`
}

type Milestone struct {
	ID          string     `json:"id"`
	PaymentID   string     `json:"payment_id"`
	DayNumber   int        `json:"day_number"`
	Amount      int64      `json:"amount"`
	Status      string     `json:"status"`
	ConfirmedBy *string    `json:"confirmed_by,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreatePayment(ctx context.Context, p *Payment) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Check if payment already exists for this job
	var existingID string
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.trx_payments WHERE job_id = $1", p.JobID).Scan(&existingID)
	if err == nil {
		return fmt.Errorf("payment already created for job %s", p.JobID)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO kerjantara.trx_payments (job_id, employer_id, worker_id, amount, platform_fee, net_to_worker, status, midtrans_order_id, midtrans_snap_token)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8)
		RETURNING id
	`, p.JobID, p.EmployerID, p.WorkerID, p.Amount, p.PlatformFee, p.NetToWorker, p.MidtransOrderID, p.MidtransSnapToken).Scan(&p.ID)

	if err != nil {
		return err
	}

	p.Status = "pending"

	// Create log event
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_payment_events (payment_id, event_type, payload, received_at)
		VALUES ($1, 'payment.created', '{}', now())
	`, p.ID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) GetPaymentByJobID(ctx context.Context, jobID string) (*Payment, error) {
	p := &Payment{}
	var snapToken sql.NullString
	var heldAt, releasedAt sql.NullTime

	err := r.db.QueryRow(ctx, `
		SELECT id, job_id, employer_id, worker_id, amount, platform_fee, net_to_worker, status, midtrans_order_id, midtrans_snap_token, held_at, released_at
		FROM kerjantara.trx_payments
		WHERE job_id = $1
	`, jobID).Scan(&p.ID, &p.JobID, &p.EmployerID, &p.WorkerID, &p.Amount, &p.PlatformFee, &p.NetToWorker, &p.Status, &p.MidtransOrderID, &snapToken, &heldAt, &releasedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if snapToken.Valid {
		p.MidtransSnapToken = &snapToken.String
	}
	if heldAt.Valid {
		p.HeldAt = &heldAt.Time
	}
	if releasedAt.Valid {
		p.ReleasedAt = &releasedAt.Time
	}

	return p, nil
}

func (r *Repository) UpdatePaymentStatus(ctx context.Context, midtransOrderID string, status string, rawPayload string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var pID string
	var currentStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM kerjantara.trx_payments 
		WHERE midtrans_order_id = $1 FOR UPDATE
	`, midtransOrderID).Scan(&pID, &currentStatus)
	
	if err != nil {
		return err
	}

	query := "UPDATE kerjantara.trx_payments SET status = $1"
	if status == "held" {
		query += ", held_at = now()"
	} else if status == "released" {
		query += ", released_at = now()"
	}
	query += " WHERE id = $2"

	_, err = tx.Exec(ctx, query, status, pID)
	if err != nil {
		return err
	}

	// Log raw event
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_payment_events (payment_id, event_type, payload, received_at)
		VALUES ($1, $2, $3, now())
	`, pID, "webhook."+status, rawPayload)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) ReleaseEscrow(ctx context.Context, jobID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var pID string
	var workerID string
	var status string
	var netToWorker int64

	err = tx.QueryRow(ctx, `
		SELECT id, worker_id, status, net_to_worker 
		FROM kerjantara.trx_payments 
		WHERE job_id = $1 FOR UPDATE
	`, jobID).Scan(&pID, &workerID, &status, &netToWorker)

	if err != nil {
		return err
	}

	if status == "released" {
		return nil // already released
	}

	// Update status payment
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.trx_payments
		SET status = 'released', released_at = now()
		WHERE id = $1
	`, pID)
	if err != nil {
		return err
	}

	// Update KitaDompet balance worker
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.mst_worker_profiles
		SET kitadompet_balance = kitadompet_balance + $1, updated_at = now()
		WHERE user_id = $2
	`, netToWorker, workerID)
	if err != nil {
		return err
	}

	// Log audit event
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_payment_events (payment_id, event_type, payload, received_at)
		VALUES ($1, 'escrow.released', '{}', now())
	`, pID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) CreateMilestones(ctx context.Context, paymentID string, durationDays int, totalAmount int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Pastikan tabel trx_payment_milestones ada
	_, _ = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS kerjantara.trx_payment_milestones (
			id           UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			payment_id   UUID         NOT NULL REFERENCES kerjantara.trx_payments(id),
			day_number   SMALLINT     NOT NULL,
			amount       BIGINT       NOT NULL,
			status       VARCHAR(20)  NOT NULL DEFAULT 'pending',
			confirmed_by UUID         REFERENCES kerjantara.mst_users(id),
			released_at  TIMESTAMPTZ,
			UNIQUE(payment_id, day_number)
		);
	`)

	// Bagi rata amount per hari
	dailyAmount := totalAmount / int64(durationDays)
	remainder := totalAmount % int64(durationDays)

	for day := 1; day <= durationDays; day++ {
		amount := dailyAmount
		if day == durationDays {
			// Tambahkan sisa pembagian ke hari terakhir
			amount += remainder
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO kerjantara.trx_payment_milestones (payment_id, day_number, amount, status)
			VALUES ($1, $2, $3, 'pending')
		`, paymentID, day, amount)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (r *Repository) ReleaseMilestone(ctx context.Context, jobID string, dayNumber int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var pID string
	var workerID string
	var employerID string
	var status string

	err = tx.QueryRow(ctx, `
		SELECT id, worker_id, employer_id, status 
		FROM kerjantara.trx_payments 
		WHERE job_id = $1
	`, jobID).Scan(&pID, &workerID, &employerID, &status)
	if err != nil {
		return err
	}

	// Ambil detail milestone untuk dayNumber
	var milestoneID string
	var mAmount int64
	var mStatus string

	err = tx.QueryRow(ctx, `
		SELECT id, amount, status FROM kerjantara.trx_payment_milestones
		WHERE payment_id = $1 AND day_number = $2 FOR UPDATE
	`, pID, dayNumber).Scan(&milestoneID, &mAmount, &mStatus)
	if err != nil {
		return err
	}

	if mStatus == "released" {
		return nil // already released
	}

	// Update status milestone
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.trx_payment_milestones
		SET status = 'released', confirmed_by = $1, released_at = now()
		WHERE id = $2
	`, employerID, milestoneID)
	if err != nil {
		return err
	}

	// Update KitaDompet balance worker
	// Milestone dilepaskan secara proporsional. Platform fee dipotong di akhir atau dipotong flat per hari?
	// Untuk kemudahan, kita asumsikan net_to_worker milestone = mAmount (dikurangi porsi platform fee jika dihitung prorata).
	// Di MVP, kita rilis mAmount langsung ke balance worker.
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.mst_worker_profiles
		SET kitadompet_balance = kitadompet_balance + $1, updated_at = now()
		WHERE user_id = $2
	`, mAmount, workerID)
	if err != nil {
		return err
	}

	// Log audit event
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_payment_events (payment_id, event_type, payload, received_at)
		VALUES ($1, 'milestone.released', $2, now())
	`, pID, fmt.Sprintf(`{"day_number": %d, "amount": %d}`, dayNumber, mAmount))
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) GetMilestones(ctx context.Context, jobID string) ([]Milestone, error) {
	// Dapatkan payment_id
	var paymentID string
	err := r.db.QueryRow(ctx, "SELECT id FROM kerjantara.trx_payments WHERE job_id = $1", jobID).Scan(&paymentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []Milestone{}, nil
		}
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, payment_id, day_number, amount, status, COALESCE(confirmed_by::text, '') as confirmed_by, released_at
		FROM kerjantara.trx_payment_milestones
		WHERE payment_id = $1
		ORDER BY day_number ASC
	`, paymentID)
	if err != nil {
		// Jika tabel belum terbuat
		return []Milestone{}, nil
	}
	defer rows.Close()

	var result []Milestone
	for rows.Next() {
		var m Milestone
		var confBy sql.NullString
		var relAt sql.NullTime
		err := rows.Scan(&m.ID, &m.PaymentID, &m.DayNumber, &m.Amount, &m.Status, &confBy, &relAt)
		if err != nil {
			return nil, err
		}
		if confBy.Valid && confBy.String != "" {
			m.ConfirmedBy = &confBy.String
		}
		if relAt.Valid {
			m.ReleasedAt = &relAt.Time
		}
		result = append(result, m)
	}

	return result, nil
}
