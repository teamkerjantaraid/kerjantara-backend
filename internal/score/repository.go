package score

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WorkerScoreData struct {
	WorkerID        string             `json:"worker_id"`
	FullName        string             `json:"full_name"`
	KerjantaraScore float64            `json:"kerjantara_score"`
	TotalJobsDone   int                `json:"total_jobs_done"`
	History         []ScoreHistoryItem `json:"history,omitempty"`
}

type ScoreHistoryItem struct {
	ScoreBefore float64   `json:"score_before"`
	ScoreAfter  float64   `json:"score_after"`
	Delta       float64   `json:"delta"`
	JobID       string    `json:"job_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetWorkerScore(ctx context.Context, workerID string) (*WorkerScoreData, error) {
	wsd := &WorkerScoreData{WorkerID: workerID}

	err := r.db.QueryRow(ctx, `
		SELECT u.full_name, wp.kerjantara_score, wp.total_jobs_done
		FROM kerjantara.mst_users u
		JOIN kerjantara.mst_worker_profiles wp ON wp.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, workerID).Scan(&wsd.FullName, &wsd.KerjantaraScore, &wsd.TotalJobsDone)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return wsd, nil
}

func (r *Repository) GetScoreHistory(ctx context.Context, workerID string) ([]ScoreHistoryItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT score_before, score_after, delta, COALESCE(triggered_by_job::text, '') as job_id, created_at
		FROM kerjantara.log_kerjantara_score_history
		WHERE worker_id = $1
		ORDER BY created_at DESC
	`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []ScoreHistoryItem
	for rows.Next() {
		var item ScoreHistoryItem
		var jobIDStr string
		err := rows.Scan(&item.ScoreBefore, &item.ScoreAfter, &item.Delta, &jobIDStr, &item.CreatedAt)
		if err != nil {
			return nil, err
		}
		item.JobID = jobIDStr
		history = append(history, item)
	}

	return history, nil
}

func (r *Repository) GetAverageRating(ctx context.Context, workerID string) (float64, error) {
	var avgRating sql.NullFloat64
	err := r.db.QueryRow(ctx, `
		SELECT AVG(score)
		FROM kerjantara.trx_ratings
		WHERE ratee_id = $1
	`, workerID).Scan(&avgRating)

	if err != nil {
		return 0, err
	}

	if avgRating.Valid {
		return avgRating.Float64, nil
	}

	return 0.0, nil
}

func (r *Repository) UpdateWorkerScoreAndLog(ctx context.Context, workerID string, newScore float64, delta float64, jobID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Get current score
	var oldScore float64
	err = tx.QueryRow(ctx, `
		SELECT kerjantara_score 
		FROM kerjantara.mst_worker_profiles
		WHERE user_id = $1 FOR UPDATE
	`, workerID).Scan(&oldScore)
	if err != nil {
		return err
	}

	// Update score
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.mst_worker_profiles
		SET kerjantara_score = $1, updated_at = now()
		WHERE user_id = $2
	`, newScore, workerID)
	if err != nil {
		return err
	}

	// Insert history
	var jobVal interface{} = nil
	if jobID != "" {
		jobVal = jobID
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_kerjantara_score_history (worker_id, score_before, score_after, delta, triggered_by_job, created_at)
		VALUES ($1, $2, $3, $4, $5, now())
	`, workerID, oldScore, newScore, delta, jobVal)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) IncrementTotalJobsDone(ctx context.Context, workerID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE kerjantara.mst_worker_profiles
		SET total_jobs_done = total_jobs_done + 1, updated_at = now()
		WHERE user_id = $1
	`, workerID)
	return err
}
