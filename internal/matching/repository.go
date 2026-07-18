package matching

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type JobInfo struct {
	ID                 string
	EmployerID         string
	SkillCatID         int
	ScheduledStartDate time.Time
	DurationDays       int
	Budget             int64
	Lat                float64
	Lng                float64
	CityCode           string
}

type Candidate struct {
	MatchID         string
	WorkerID        string
	FullName        string
	Phone           string
	Bio             *string
	DistanceMeters  float64
	KerjantaraScore float64
	TotalJobsDone   int
	AvgResponseMin  float64
	CompositeScore  float64
}

type JobMatch struct {
	ID             string    `json:"id"`
	JobID          string
	WorkerID       string
	CompositeScore float64
	Breakdown      string // JSON string
	Rank           int
	Deadline       time.Time
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetJobDetails(ctx context.Context, jobID string) (*JobInfo, error) {
	j := &JobInfo{}
	var startDate sql.NullTime

	// duration_days dan scheduled_start_date mungkin butuh default jika kosong di DB.
	// Di skema: duration_days SMALLINT DEFAULT 1, scheduled_start_date DATE DEFAULT CURRENT_DATE
	// Namun, di migration sql: scheduled_start_date ada di Arsitektur_Monolith butuh ditambahkan
	// jika belum ada. Kita pastikan query menangani fallback jika kolom tidak ada / null.
	err := r.db.QueryRow(ctx, `
		SELECT id, employer_id, skill_cat_id, budget, city_code,
		       ST_Y(location::geometry) as lat, ST_X(location::geometry) as lng
		FROM kerjantara.trx_jobs
		WHERE id = $1
	`, jobID).Scan(&j.ID, &j.EmployerID, &j.SkillCatID, &j.Budget, &j.CityCode, &j.Lat, &j.Lng)

	if err != nil {
		return nil, err
	}

	// scheduled_start_date dan duration_days mungkin tidak didefinisikan di schema SQL awal (004_create_trx_tables.sql).
	// Di v3.3 Arsitektur, scheduled_start_date DATE dan duration_days SMALLINT ditambahkan.
	// Mari kita load duration_days dan scheduled_start_date dengan query dinamis / fallback aman.
	var durationDays int16 = 1
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(duration_days, 1), scheduled_start_date
		FROM kerjantara.trx_jobs
		WHERE id = $1
	`, jobID).Scan(&durationDays, &startDate)
	
	if err == nil {
		j.DurationDays = int(durationDays)
		if startDate.Valid {
			j.ScheduledStartDate = startDate.Time
		} else {
			j.ScheduledStartDate = time.Now()
		}
	} else {
		// Fallback jika kolom belum terbuat di migrasi
		j.DurationDays = 1
		j.ScheduledStartDate = time.Now()
	}

	return j, nil
}

func (r *Repository) GetCandidatesForJob(ctx context.Context, job *JobInfo, radiusMeter float64) ([]Candidate, error) {
	// Query PostGIS mencari kandidat pekerja terdekat
	// Filter: skill_cat_id cocok, is_available = true, approved KTP, bukan employer itu sendiri, akun user is_active
	// Dan TIDAK overlap jadwal: check m.match_status = 'accepted' & j.status = 'ongoing'
	// yang rentang tanggalnya bentrok dengan job baru ini.
	
	query := `
		SELECT u.id, u.full_name, u.phone, wp.bio, wp.kerjantara_score, wp.total_jobs_done, wp.avg_response_min,
		       ST_Distance(u.location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) as distance_meters
		FROM kerjantara.mst_users u
		JOIN kerjantara.mst_worker_profiles wp ON wp.user_id = u.id
		JOIN kerjantara.ref_verif_statuses vs ON u.verif_status_id = vs.id
		WHERE u.id != $3
		  AND u.is_active = true
		  AND wp.is_available = true
		  AND vs.code = 'approved'
		  AND EXISTS (
		      SELECT 1 FROM kerjantara.mst_worker_skills ws
		      WHERE ws.worker_id = u.id AND ws.skill_cat_id = $4
		  )
		  AND ST_DWithin(u.location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $5)
		  -- Exclude overlapping commitments
		  AND NOT EXISTS (
		      SELECT 1 FROM kerjantara.trx_job_matches jm
		      JOIN kerjantara.trx_jobs j ON j.id = jm.job_id
		      JOIN kerjantara.ref_job_statuses js ON j.status_id = js.id
		      WHERE jm.worker_id = u.id
		        AND jm.match_status = 'accepted'
		        AND js.code = 'ongoing'
		        -- Overlap date logic: (start_date1 <= end_date2) AND (end_date1 >= start_date2)
		        AND (j.scheduled_start_date <= ($6::date + ($7::int * INTERVAL '1 day')::interval))
		        AND ($6::date <= (j.scheduled_start_date + (j.duration_days * INTERVAL '1 day')::interval))
		  )
		ORDER BY distance_meters ASC
	`

	rows, err := r.db.Query(ctx, query, job.Lng, job.Lat, job.EmployerID, job.SkillCatID, radiusMeter, job.ScheduledStartDate, job.DurationDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []Candidate
	for rows.Next() {
		var c Candidate
		var bio sql.NullString
		err := rows.Scan(&c.WorkerID, &c.FullName, &c.Phone, &bio, &c.KerjantaraScore, &c.TotalJobsDone, &c.AvgResponseMin, &c.DistanceMeters)
		if err != nil {
			return nil, err
		}
		if bio.Valid {
			c.Bio = &bio.String
		}
		candidates = append(candidates, c)
	}

	return candidates, nil
}

func (r *Repository) GetCandidatesForJobByCity(ctx context.Context, job *JobInfo, cityID int) ([]Candidate, error) {
	// Fallback ke kota terdekat
	// Filter location by city boundary (diwakili user.city_id = cityID)
	// Kita sorting jarak dari centroid kota ke lokasi job
	query := `
		SELECT u.id, u.full_name, u.phone, wp.bio, wp.kerjantara_score, wp.total_jobs_done, wp.avg_response_min,
		       ST_Distance(u.location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) as distance_meters
		FROM kerjantara.mst_users u
		JOIN kerjantara.mst_worker_profiles wp ON wp.user_id = u.id
		JOIN kerjantara.ref_verif_statuses vs ON u.verif_status_id = vs.id
		WHERE u.id != $3
		  AND u.is_active = true
		  AND wp.is_available = true
		  AND vs.code = 'approved'
		  AND u.city_id = $4
		  AND EXISTS (
		      SELECT 1 FROM kerjantara.mst_worker_skills ws
		      WHERE ws.worker_id = u.id AND ws.skill_cat_id = $5
		  )
		  -- Exclude overlapping commitments
		  AND NOT EXISTS (
		      SELECT 1 FROM kerjantara.trx_job_matches jm
		      JOIN kerjantara.trx_jobs j ON j.id = jm.job_id
		      JOIN kerjantara.ref_job_statuses js ON j.status_id = js.id
		      WHERE jm.worker_id = u.id
		        AND jm.match_status = 'accepted'
		        AND js.code = 'ongoing'
		        AND (j.scheduled_start_date <= ($6::date + ($7::int * INTERVAL '1 day')::interval))
		        AND ($6::date <= (j.scheduled_start_date + (j.duration_days * INTERVAL '1 day')::interval))
		  )
		ORDER BY distance_meters ASC
	`

	rows, err := r.db.Query(ctx, query, job.Lng, job.Lat, job.EmployerID, cityID, job.SkillCatID, job.ScheduledStartDate, job.DurationDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []Candidate
	for rows.Next() {
		var c Candidate
		var bio sql.NullString
		err := rows.Scan(&c.WorkerID, &c.FullName, &c.Phone, &bio, &c.KerjantaraScore, &c.TotalJobsDone, &c.AvgResponseMin, &c.DistanceMeters)
		if err != nil {
			return nil, err
		}
		if bio.Valid {
			c.Bio = &bio.String
		}
		candidates = append(candidates, c)
	}

	return candidates, nil
}

func (r *Repository) SaveJobMatches(ctx context.Context, matches []JobMatch) ([]JobMatch, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var result []JobMatch
	for _, m := range matches {
		var matchID string
		err = tx.QueryRow(ctx, `
			INSERT INTO kerjantara.trx_job_matches (job_id, worker_id, composite_score, score_breakdown, match_rank, match_status, response_deadline, notified_at)
			VALUES ($1, $2, $3, $4, $5, 'recommended', $6, now())
			ON CONFLICT (job_id, worker_id) DO UPDATE
			SET composite_score = EXCLUDED.composite_score,
			    score_breakdown = EXCLUDED.score_breakdown,
			    match_rank = EXCLUDED.match_rank,
			    match_status = 'recommended',
			    response_deadline = EXCLUDED.response_deadline,
			    notified_at = now()
			RETURNING id
		`, m.JobID, m.WorkerID, m.CompositeScore, m.Breakdown, m.Rank, m.Deadline).Scan(&matchID)
		if err != nil {
			return nil, err
		}
		m.ID = matchID
		result = append(result, m)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) UpdateJobStatus(ctx context.Context, jobID string, statusCode string) error {
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

	// Get current status
	var oldStatusID sql.NullInt16
	err = tx.QueryRow(ctx, "SELECT status_id FROM kerjantara.trx_jobs WHERE id = $1", jobID).Scan(&oldStatusID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.trx_jobs
		SET status_id = $1
		WHERE id = $2
	`, statusID, jobID)
	if err != nil {
		return err
	}

	// Insert history
	var oldVal interface{} = nil
	if oldStatusID.Valid {
		oldVal = oldStatusID.Int16
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.log_job_status_history (job_id, from_status_id, to_status_id, changed_by, changed_at)
		VALUES ($1, $2, $3, NULL, now())
	`, jobID, oldVal, statusID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
