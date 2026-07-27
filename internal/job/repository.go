package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Job struct {
	ID                 string     `json:"job_id"`
	EmployerID         string     `json:"employer_id"`
	EmployerName       string     `json:"employer_name,omitempty"`
	SkillCatID         int        `json:"skill_cat_id"`
	SkillCatCode       string     `json:"skill_cat_code,omitempty"`
	SkillCatLabel      string     `json:"skill_cat_label,omitempty"`
	StatusID           int        `json:"status_id"`
	Status             string     `json:"status"`
	Description        string     `json:"description"`
	Budget             int64      `json:"budget_max"` // di DB kolom 'budget'
	AgreedPrice        *int64     `json:"agreed_price,omitempty"`
	SearchRadiusKM     float64    `json:"search_radius_km"`
	Lat                float64    `json:"lat"`
	Lng                float64    `json:"lng"`
	CityCode           string     `json:"city_code"`
	DurationDays       int        `json:"duration_days"`
	ScheduledStartDate time.Time  `json:"scheduled_start_date"`
	PostedAt           time.Time  `json:"posted_at"`
	ExpiresAt          time.Time  `json:"expires_at"`
	PriceAcceptedAt    *time.Time `json:"price_accepted_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	AcceptedWorker     *WorkerInfo `json:"accepted_worker,omitempty"`
	PaymentStatus      string     `json:"payment_status,omitempty"`
}

type WorkerInfo struct {
	UserID          string  `json:"user_id"`
	FullName        string  `json:"full_name"`
	KerjantaraScore float64 `json:"kerjantara_score"`
}

type RateCard struct {
	SkillCatID     int       `json:"skill_cat_id"`
	SkillCatLabel  string    `json:"skill_cat_label"`
	CityCode       string    `json:"city_code"`
	MinRate        int64     `json:"min_rate"`
	MaxRate        int64     `json:"max_rate"`
	RateUnit       string    `json:"rate_unit"`
	Label          string    `json:"label"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SkillCategory struct {
	ID       int              `json:"id"`
	Code     string           `json:"code"`
	Label    string           `json:"label"`
	Children []SkillCategory  `json:"children,omitempty"`
	ParentID *int             `json:"-"`
	IconKey  string           `json:"icon_key,omitempty"`
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetStatusIDByCode(ctx context.Context, code string) (int, error) {
	var id int
	err := r.db.QueryRow(ctx, "SELECT id FROM kerjantara.ref_job_statuses WHERE code = $1", code).Scan(&id)
	return id, err
}

type WorkerVerification struct {
	VerifStatus string `json:"verif_status"`
	IsAvailable bool   `json:"is_available"`
}

func (r *Repository) GetWorkerVerification(ctx context.Context, workerID string) (*WorkerVerification, error) {
	v := &WorkerVerification{}
	err := r.db.QueryRow(ctx, `
		SELECT vs.code, COALESCE(wp.is_available, false)
		FROM kerjantara.mst_users u
		JOIN kerjantara.ref_verif_statuses vs ON u.verif_status_id = vs.id
		LEFT JOIN kerjantara.mst_worker_profiles wp ON wp.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, workerID).Scan(&v.VerifStatus, &v.IsAvailable)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return v, nil
}

func (r *Repository) CreateJob(ctx context.Context, j *Job) (*Job, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Get pending status ID
	var statusID int
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_job_statuses WHERE code = 'pending'").Scan(&statusID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending status: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(2 * time.Hour) // default expires in 2 hours

	var jobID string
	err = tx.QueryRow(ctx, `
		INSERT INTO kerjantara.trx_jobs (employer_id, skill_cat_id, status_id, description, budget, search_radius_km, location, city_code, posted_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 2.0, ST_SetSRID(ST_MakePoint($6, $7), 4326)::geography, $8, $9, $10)
		RETURNING id
	`, j.EmployerID, j.SkillCatID, statusID, j.Description, j.Budget, j.Lng, j.Lat, j.CityCode, now, expiresAt).Scan(&jobID)

	if err != nil {
		return nil, fmt.Errorf("failed to insert job: %w", err)
	}

	j.ID = jobID
	j.StatusID = statusID
	j.Status = "pending"
	j.PostedAt = now
	j.ExpiresAt = expiresAt
	j.SearchRadiusKM = 2.0

	// Insert history
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_job_status_history (job_id, from_status_id, to_status_id, changed_by, changed_at)
		VALUES ($1, NULL, $2, $3, now())
	`, jobID, statusID, j.EmployerID)
	if err != nil {
		return nil, fmt.Errorf("failed to log status history: %w", err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}

	return j, nil
}

func (r *Repository) GetJobByID(ctx context.Context, id string) (*Job, error) {
	j := &Job{}
	var agreedPrice sql.NullInt64
	var acceptedAt, completedAt sql.NullTime
	var workerID, workerName sql.NullString
	var workerScore sql.NullFloat64
	var paymentStatus sql.NullString

	query := `
		SELECT j.id, j.employer_id, ue.full_name as employer_name, j.skill_cat_id, sc.code as skill_cat_code, sc.label_id as skill_cat_label, 
		       j.status_id, js.code as status_code, j.description, j.budget, j.agreed_price, j.search_radius_km,
		       ST_Y(j.location::geometry) as lat, ST_X(j.location::geometry) as lng, j.city_code,
		       j.duration_days, j.posted_at, j.expires_at, j.price_accepted_at, j.completed_at,
		       uw.id as worker_id, uw.full_name as worker_name, wp.kerjantara_score as worker_score,
		       pay.status as payment_status
		FROM kerjantara.trx_jobs j
		JOIN kerjantara.mst_users ue ON j.employer_id = ue.id
		JOIN kerjantara.ref_skill_categories sc ON j.skill_cat_id = sc.id
		JOIN kerjantara.ref_job_statuses js ON j.status_id = js.id
		LEFT JOIN kerjantara.trx_job_matches jm ON jm.job_id = j.id AND jm.match_status = 'accepted'
		LEFT JOIN kerjantara.mst_users uw ON jm.worker_id = uw.id
		LEFT JOIN kerjantara.mst_worker_profiles wp ON wp.user_id = uw.id
		LEFT JOIN kerjantara.trx_payments pay ON pay.job_id = j.id
		WHERE j.id = $1 AND j.deleted_at IS NULL
	`

	err := r.db.QueryRow(ctx, query, id).Scan(
		&j.ID, &j.EmployerID, &j.EmployerName, &j.SkillCatID, &j.SkillCatCode, &j.SkillCatLabel,
		&j.StatusID, &j.Status, &j.Description, &j.Budget, &agreedPrice, &j.SearchRadiusKM,
		&j.Lat, &j.Lng, &j.CityCode, &j.DurationDays, &j.PostedAt, &j.ExpiresAt, &acceptedAt, &completedAt,
		&workerID, &workerName, &workerScore, &paymentStatus,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if agreedPrice.Valid {
		j.AgreedPrice = &agreedPrice.Int64
	}
	if acceptedAt.Valid {
		j.PriceAcceptedAt = &acceptedAt.Time
	}
	if completedAt.Valid {
		j.CompletedAt = &completedAt.Time
	}
	if workerID.Valid {
		j.AcceptedWorker = &WorkerInfo{
			UserID:          workerID.String,
			FullName:        workerName.String,
			KerjantaraScore: workerScore.Float64,
		}
	}
	if paymentStatus.Valid {
		j.PaymentStatus = paymentStatus.String
	} else {
		j.PaymentStatus = "pending"
	}

	return j, nil
}

func (r *Repository) AcceptJobMatch(ctx context.Context, jobID string, workerID string, matchID string) (*Job, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. Row Lock FOR UPDATE pada trx_jobs
	var statusID int
	var statusCode string
	var budget int64
	err = tx.QueryRow(ctx, `
		SELECT status_id, js.code, budget 
		FROM kerjantara.trx_jobs j
		JOIN kerjantara.ref_job_statuses js ON j.status_id = js.id
		WHERE j.id = $1 FOR UPDATE
	`, jobID).Scan(&statusID, &statusCode, &budget)

	if err != nil {
		return nil, err
	}

	// 2. Validasi status harus 'matched'
	if statusCode != "matched" {
		return nil, errors.New("JOB_TAKEN") // Status sudah berubah, diambil orang lain atau kedaluwarsa
	}

	// Get 'accepted' status ID
	var acceptedStatusID int
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_job_statuses WHERE code = 'accepted'").Scan(&acceptedStatusID)
	if err != nil {
		return nil, err
	}

	// 3. Update match_status = 'accepted' untuk worker yang klik
	res, err := tx.Exec(ctx, `
		UPDATE kerjantara.trx_job_matches
		SET match_status = 'accepted', responded_at = now()
		WHERE id = $1 AND worker_id = $2
	`, matchID, workerID)
	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		return nil, errors.New("match tidak ditemukan")
	}

	// 4. Update match_status = 'rejected' untuk rank lain
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.trx_job_matches
		SET match_status = 'rejected', responded_at = now()
		WHERE job_id = $1 AND worker_id != $2
	`, jobID, workerID)
	if err != nil {
		return nil, err
	}

	// 5. Update trx_jobs agreed_price & status = 'accepted'
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.trx_jobs
		SET status_id = $1, agreed_price = budget, price_accepted_at = now()
		WHERE id = $2
	`, acceptedStatusID, jobID)
	if err != nil {
		return nil, err
	}

	// 6. Insert log match response
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_match_responses (match_id, job_id, worker_id, response, responded_at)
		VALUES ($1, $2, $3, 'accepted', now())
	`, matchID, jobID, workerID)
	if err != nil {
		return nil, err
	}

	// 7. Insert log job status history
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_job_status_history (job_id, from_status_id, to_status_id, changed_by, changed_at)
		VALUES ($1, $2, $3, $4, now())
	`, jobID, statusID, acceptedStatusID, workerID)
	if err != nil {
		return nil, err
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}

	return r.GetJobByID(ctx, jobID)
}

func (r *Repository) RejectJobMatch(ctx context.Context, jobID string, workerID string, matchID string) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	// Update match_status = 'rejected'
	res, err := tx.Exec(ctx, `
		UPDATE kerjantara.trx_job_matches
		SET match_status = 'rejected', responded_at = now()
		WHERE id = $1 AND worker_id = $2
	`, matchID, workerID)
	if err != nil {
		return false, err
	}
	if res.RowsAffected() == 0 {
		return false, errors.New("match tidak ditemukan")
	}

	// Insert log
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_match_responses (match_id, job_id, worker_id, response, responded_at)
		VALUES ($1, $2, $3, 'rejected', now())
	`, matchID, jobID, workerID)
	if err != nil {
		return false, err
	}

	// Cek apakah ada rank berikutnya yang masih pending (jika rank 1 reject -> notif rank 2, dst)
	// Namun di MVP kita cek apakah semua matches sudah merespons.
	var anyPending bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM kerjantara.trx_job_matches
			WHERE job_id = $1 AND match_status = 'recommended'
		)
	`, jobID).Scan(&anyPending)
	if err != nil {
		return false, err
	}

	if !anyPending {
		// Jika semua reject/timeout -> set status job ke 'no_takers'
		var statusID int
		err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_job_statuses WHERE code = 'no_takers'").Scan(&statusID)
		if err != nil {
			// fallback to expired if no_takers status code not found
			_ = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_job_statuses WHERE code = 'expired'").Scan(&statusID)
		}

		// Update job
		_, err = tx.Exec(ctx, "UPDATE kerjantara.trx_jobs SET status_id = $1 WHERE id = $2", statusID, jobID)
		if err != nil {
			return false, err
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		return false, err
	}

	return anyPending, nil
}

func (r *Repository) UpdateJobStatusAndLog(ctx context.Context, jobID string, statusCode string, changedBy string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var statusID int
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_job_statuses WHERE code = $1", statusCode).Scan(&statusID)
	if err != nil {
		return err
	}

	var oldStatusID sql.NullInt16
	err = tx.QueryRow(ctx, "SELECT status_id FROM kerjantara.trx_jobs WHERE id = $1", jobID).Scan(&oldStatusID)
	if err != nil {
		return err
	}

	// Update job
	query := "UPDATE kerjantara.trx_jobs SET status_id = $1"
	if statusCode == "done" {
		query += ", completed_at = now()"
	}
	query += " WHERE id = $2"

	_, err = tx.Exec(ctx, query, statusID, jobID)
	if err != nil {
		return err
	}

	// Insert history log
	var oldVal interface{} = nil
	if oldStatusID.Valid {
		oldVal = oldStatusID.Int16
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_job_status_history (job_id, from_status_id, to_status_id, changed_by, changed_at)
		VALUES ($1, $2, $3, $4, now())
	`, jobID, oldVal, statusID, changedBy)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) GetJobsByEmployer(ctx context.Context, employerID string, statusCode string, limit, offset int) ([]Job, int, error) {
	var rows pgx.Rows
	var err error

	baseQuery := `
		FROM kerjantara.trx_jobs j
		JOIN kerjantara.ref_skill_categories sc ON j.skill_cat_id = sc.id
		JOIN kerjantara.ref_job_statuses js ON j.status_id = js.id
		WHERE j.employer_id = $1 AND j.deleted_at IS NULL
	`

	params := []interface{}{employerID}
	queryCount := "SELECT COUNT(*) " + baseQuery
	querySelect := `
		SELECT j.id, j.employer_id, j.skill_cat_id, sc.code, sc.label_id, j.status_id, js.code, j.description, j.budget, j.agreed_price,
		       ST_Y(j.location::geometry), ST_X(j.location::geometry), j.city_code, j.posted_at, j.expires_at, j.completed_at
	` + baseQuery

	if statusCode != "" {
		params = append(params, statusCode)
		queryCount += " AND js.code = $2"
		querySelect += " AND js.code = $2"
	}

	var total int
	err = r.db.QueryRow(ctx, queryCount, params...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	querySelect += fmt.Sprintf(" ORDER BY j.posted_at DESC LIMIT $%d OFFSET $%d", len(params)+1, len(params)+2)
	params = append(params, limit, offset)

	rows, err = r.db.Query(ctx, querySelect, params...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var agreedPrice sql.NullInt64
		var completedAt sql.NullTime
		err := rows.Scan(
			&j.ID, &j.EmployerID, &j.SkillCatID, &j.SkillCatCode, &j.SkillCatLabel, &j.StatusID, &j.Status, &j.Description, &j.Budget, &agreedPrice,
			&j.Lat, &j.Lng, &j.CityCode, &j.PostedAt, &j.ExpiresAt, &completedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		if agreedPrice.Valid {
			j.AgreedPrice = &agreedPrice.Int64
		}
		if completedAt.Valid {
			j.CompletedAt = &completedAt.Time
		}
		jobs = append(jobs, j)
	}

	return jobs, total, nil
}

func (r *Repository) GetJobsByWorker(ctx context.Context, workerID string, statusCode string, limit, offset int) ([]Job, int, error) {
	// Worker melihat list job history berdasarkan match yang di-accept oleh worker tsb
	baseQuery := `
		FROM kerjantara.trx_jobs j
		JOIN kerjantara.ref_skill_categories sc ON j.skill_cat_id = sc.id
		JOIN kerjantara.ref_job_statuses js ON j.status_id = js.id
		JOIN kerjantara.trx_job_matches jm ON jm.job_id = j.id
		WHERE jm.worker_id = $1 AND jm.match_status = 'accepted' AND j.deleted_at IS NULL
	`

	params := []interface{}{workerID}
	queryCount := "SELECT COUNT(*) " + baseQuery
	querySelect := `
		SELECT j.id, j.employer_id, j.skill_cat_id, sc.code, sc.label_id, j.status_id, js.code, j.description, j.budget, j.agreed_price,
		       ST_Y(j.location::geometry), ST_X(j.location::geometry), j.city_code, j.posted_at, j.expires_at, j.completed_at
	` + baseQuery

	if statusCode != "" {
		params = append(params, statusCode)
		queryCount += " AND js.code = $2"
		querySelect += " AND js.code = $2"
	}

	var total int
	err := r.db.QueryRow(ctx, queryCount, params...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	querySelect += fmt.Sprintf(" ORDER BY j.posted_at DESC LIMIT $%d OFFSET $%d", len(params)+1, len(params)+2)
	params = append(params, limit, offset)

	rows, err := r.db.Query(ctx, querySelect, params...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var agreedPrice sql.NullInt64
		var completedAt sql.NullTime
		err := rows.Scan(
			&j.ID, &j.EmployerID, &j.SkillCatID, &j.SkillCatCode, &j.SkillCatLabel, &j.StatusID, &j.Status, &j.Description, &j.Budget, &agreedPrice,
			&j.Lat, &j.Lng, &j.CityCode, &j.PostedAt, &j.ExpiresAt, &completedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		if agreedPrice.Valid {
			j.AgreedPrice = &agreedPrice.Int64
		}
		if completedAt.Valid {
			j.CompletedAt = &completedAt.Time
		}
		jobs = append(jobs, j)
	}

	return jobs, total, nil
}

func (r *Repository) SaveJobProof(ctx context.Context, jobID string, fileKeys []string, notes string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE kerjantara.trx_jobs
		SET proof_file_keys = $1, proof_notes = $2, status_id = (SELECT id FROM kerjantara.ref_job_statuses WHERE code = 'done'), completed_at = now()
		WHERE id = $3
	`, fileKeys, notes, jobID)
	return err
}

type DayLog struct {
	ID            string     `json:"id"`
	JobID         string     `json:"job_id"`
	DayNumber     int        `json:"day_number"`
	ProofFileKeys []string   `json:"proof_file_keys,omitempty"`
	ProofNotes    string     `json:"proof_notes,omitempty"`
	CompletedAt   time.Time  `json:"completed_at"`
	ConfirmedBy   *string    `json:"confirmed_by,omitempty"`
	ConfirmedAt   *time.Time `json:"confirmed_at,omitempty"`
}

func (r *Repository) EnsureDayLogsTable(ctx context.Context) {
	r.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS kerjantara.trx_job_day_logs (
			id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			job_id           UUID NOT NULL REFERENCES kerjantara.trx_jobs(id),
			day_number       SMALLINT NOT NULL,
			proof_file_keys  TEXT[],
			proof_notes      TEXT,
			completed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
			confirmed_by     UUID REFERENCES kerjantara.mst_users(id),
			confirmed_at     TIMESTAMPTZ,
			UNIQUE(job_id, day_number)
		)
	`)
}

func (r *Repository) SaveDayProof(ctx context.Context, jobID string, dayNumber int, fileKeys []string, notes string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO kerjantara.trx_job_day_logs (job_id, day_number, proof_file_keys, proof_notes, completed_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (job_id, day_number) DO UPDATE
		SET proof_file_keys = EXCLUDED.proof_file_keys,
		    proof_notes = EXCLUDED.proof_notes,
		    completed_at = now()
	`, jobID, dayNumber, fileKeys, notes)
	return err
}

func (r *Repository) ConfirmDayLog(ctx context.Context, jobID string, dayNumber int, confirmedBy string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO kerjantara.trx_job_day_logs (job_id, day_number, confirmed_by, confirmed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (job_id, day_number) DO UPDATE
		SET confirmed_by = EXCLUDED.confirmed_by,
		    confirmed_at = EXCLUDED.confirmed_at
	`, jobID, dayNumber, confirmedBy)
	return err
}

func (r *Repository) GetDayLog(ctx context.Context, jobID string, dayNumber int) (*DayLog, error) {
	d := &DayLog{}
	var proofFileKeys []string
	var proofNotes, confirmedBy sql.NullString
	var confirmedAt sql.NullTime

	err := r.db.QueryRow(ctx, `
		SELECT id, job_id, day_number, proof_file_keys, COALESCE(proof_notes, ''),
		       completed_at, confirmed_by, confirmed_at
		FROM kerjantara.trx_job_day_logs
		WHERE job_id = $1 AND day_number = $2
	`, jobID, dayNumber).Scan(&d.ID, &d.JobID, &d.DayNumber, &proofFileKeys,
		&proofNotes, &d.CompletedAt, &confirmedBy, &confirmedAt)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	d.ProofFileKeys = proofFileKeys
	d.ProofNotes = proofNotes.String
	if confirmedBy.Valid {
		s := confirmedBy.String
		d.ConfirmedBy = &s
	}
	if confirmedAt.Valid {
		t := confirmedAt.Time
		d.ConfirmedAt = &t
	}
	return d, nil
}

func (r *Repository) GetJobProofKeys(ctx context.Context, jobID string) ([]string, string, error) {
	var keys []string
	var notes sql.NullString
	err := r.db.QueryRow(ctx, "SELECT proof_file_keys, proof_notes FROM kerjantara.trx_jobs WHERE id = $1", jobID).Scan(&keys, &notes)
	if err != nil {
		return nil, "", err
	}
	return keys, notes.String, nil
}

func (r *Repository) SaveRating(ctx context.Context, jobID, raterID, rateeID string, score float64, comment string) (string, error) {
	var ratingID string
	err := r.db.QueryRow(ctx, `
		INSERT INTO kerjantara.trx_ratings (job_id, rater_id, ratee_id, score, comment, created_at)
		VALUES ($1, $2, $3, $4, $5, now())
		RETURNING id
	`, jobID, raterID, rateeID, score, comment).Scan(&ratingID)
	return ratingID, err
}

func (r *Repository) GetRateCard(ctx context.Context, skillCatID int, cityCode string) (*RateCard, error) {
	rc := &RateCard{}
	err := r.db.QueryRow(ctx, `
		SELECT rc.skill_cat_id, sc.label_id, rc.city_code, rc.min_rate, rc.max_rate, rc.rate_unit, rc.updated_at
		FROM kerjantara.ref_rate_cards rc
		JOIN kerjantara.ref_skill_categories sc ON rc.skill_cat_id = sc.id
		WHERE rc.skill_cat_id = $1 AND rc.city_code = $2 AND rc.is_active = true
	`, skillCatID, cityCode).Scan(&rc.SkillCatID, &rc.SkillCatLabel, &rc.CityCode, &rc.MinRate, &rc.MaxRate, &rc.RateUnit, &rc.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	rc.Label = fmt.Sprintf("Harga wajar %s di %s: Rp %d – %d/%s", rc.SkillCatLabel, rc.CityCode, rc.MinRate, rc.MaxRate, getRateUnitLabel(rc.RateUnit))
	return rc, nil
}

func (r *Repository) GetSkillCategories(ctx context.Context) ([]SkillCategory, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, parent_id, code, label_id, icon_key
		FROM kerjantara.ref_skill_categories
		WHERE is_active = true
		ORDER BY sort_order ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []SkillCategory
	for rows.Next() {
		var sc SkillCategory
		var parentID sql.NullInt16
		var iconKey sql.NullString
		err := rows.Scan(&sc.ID, &parentID, &sc.Code, &sc.Label, &iconKey)
		if err != nil {
			return nil, err
		}
		if parentID.Valid {
			p := int(parentID.Int16)
			sc.ParentID = &p
		}
		if iconKey.Valid {
			sc.IconKey = iconKey.String
		}
		all = append(all, sc)
	}

	// Build hierarchy
	var rootList []SkillCategory
	childMap := make(map[int][]SkillCategory)

	for _, sc := range all {
		if sc.ParentID != nil {
			childMap[*sc.ParentID] = append(childMap[*sc.ParentID], sc)
		}
	}

	for _, sc := range all {
		if sc.ParentID == nil {
			sc.Children = childMap[sc.ID]
			rootList = append(rootList, sc)
		}
	}

	return rootList, nil
}

func getRateUnitLabel(unit string) string {
	switch unit {
	case "per_day":
		return "hari"
	case "per_job":
		return "job"
	case "per_hour":
		return "jam"
	default:
		return unit
	}
}
