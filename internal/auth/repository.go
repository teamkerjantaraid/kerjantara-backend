package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID            string     `json:"user_id"`
	VerifStatusID int        `json:"verif_status_id"`
	VerifStatus   string     `json:"verif_status"` // code e.g. "pending", "approved"
	FullName      string     `json:"full_name"`
	Phone         string     `json:"phone"`
	PasswordHash  string     `json:"-"`
	KTPFileKey    *string    `json:"ktp_file_key,omitempty"`
	SelfieFileKey *string    `json:"selfie_file_key,omitempty"`
	IsActive      bool       `json:"is_active"`
	Lat           *float64   `json:"lat,omitempty"`
	Lng           *float64   `json:"lng,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Roles         []string   `json:"roles"`
	WorkerProfile *WorkerProfile `json:"worker_profile,omitempty"`
}

type WorkerProfile struct {
	ID                string   `json:"id"`
	UserID            string   `json:"user_id"`
	YearsExperience   int      `json:"years_experience"`
	KerjantaraScore   float64  `json:"kerjantara_score"`
	TotalJobsDone     int      `json:"total_jobs_done"`
	AvgResponseMin    float64  `json:"avg_response_min"`
	Bio               *string  `json:"bio,omitempty"`
	IsAvailable       bool     `json:"is_available"`
	KitaDompetBalance int64    `json:"kitadompet_balance"` // in cents
	Skills            []Skill  `json:"skills"`
}

type Skill struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	IsPrimary bool   `json:"is_primary"`
}

type PendingVerification struct {
	UserID       string    `json:"user_id"`
	FullName     string    `json:"full_name"`
	Phone        string    `json:"phone"`
	Role         string    `json:"role"`
	KTPPhotoURL  string    `json:"ktp_photo_url"`
	SelfieURL    string    `json:"selfie_url"`
	KTPFileKey   string    `json:"ktp_file_key"`
	SubmittedAt  time.Time `json:"submitted_at"`
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetRoleIDByCode(ctx context.Context, code string) (int, error) {
	var id int
	err := r.db.QueryRow(ctx, "SELECT id FROM kerjantara.ref_user_roles WHERE code = $1", code).Scan(&id)
	return id, err
}

func (r *Repository) GetVerifStatusIDByCode(ctx context.Context, code string) (int, error) {
	var id int
	err := r.db.QueryRow(ctx, "SELECT id FROM kerjantara.ref_verif_statuses WHERE code = $1", code).Scan(&id)
	return id, err
}

func (r *Repository) CreateUser(ctx context.Context, u *User, initialRoleCode string) (*User, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Get initial verif status: 'pending' (atau bisa 'approved' untuk admin, tapi default 'pending')
	var verifStatusID int
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_verif_statuses WHERE code = 'pending'").Scan(&verifStatusID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending status: %w", err)
	}

	// Insert user
	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO kerjantara.mst_users (verif_status_id, full_name, phone, password_hash, is_active)
		VALUES ($1, $2, $3, $4, true)
		RETURNING id, created_at, updated_at
	`, verifStatusID, u.FullName, u.Phone, u.PasswordHash).Scan(&userID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert user: %w", err)
	}

	u.ID = userID
	u.VerifStatusID = verifStatusID
	u.VerifStatus = "pending"
	u.IsActive = true

	// Get role ID
	var roleID int
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_user_roles WHERE code = $1", initialRoleCode).Scan(&roleID)
	if err != nil {
		return nil, fmt.Errorf("failed to get role ID: %w", err)
	}

	// Insert mst_user_roles
	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.mst_user_roles (user_id, role_id, is_primary)
		VALUES ($1, $2, true)
	`, userID, roleID)
	if err != nil {
		return nil, fmt.Errorf("failed to insert user role: %w", err)
	}

	// DONT create worker profile yet, it's lazy created when worker role is activated
	// OR if initial role is worker, but wait, schema says:
	// "mst_worker_profiles: Dibuat otomatis saat user register dengan role worker"
	// Mari kita buat jika role adalah worker
	if initialRoleCode == "worker" {
		_, err = tx.Exec(ctx, `
			INSERT INTO kerjantara.mst_worker_profiles (user_id, kerjantara_score, total_jobs_done, avg_response_min, is_available)
			VALUES ($1, 0.00, 0, 0, false)
		`, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to insert worker profile: %w", err)
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}

	u.Roles = []string{initialRoleCode}
	return u, nil
}

func (r *Repository) GetUserByPhone(ctx context.Context, phone string) (*User, error) {
	u := &User{}
	var lat, lng sql.NullFloat64

	err := r.db.QueryRow(ctx, `
		SELECT u.id, u.verif_status_id, s.code, u.full_name, u.phone, u.password_hash, 
		       u.ktp_file_key, u.selfie_file_key, u.is_active, 
		       ST_Y(u.location::geometry) as lat, ST_X(u.location::geometry) as lng,
		       u.created_at, u.updated_at
		FROM kerjantara.mst_users u
		JOIN kerjantara.ref_verif_statuses s ON u.verif_status_id = s.id
		WHERE u.phone = $1 AND u.deleted_at IS NULL
	`, phone).Scan(&u.ID, &u.VerifStatusID, &u.VerifStatus, &u.FullName, &u.Phone, &u.PasswordHash,
		&u.KTPFileKey, &u.SelfieFileKey, &u.IsActive, &lat, &lng, &u.CreatedAt, &u.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if lat.Valid {
		u.Lat = &lat.Float64
	}
	if lng.Valid {
		u.Lng = &lng.Float64
	}

	// Get Roles
	rows, err := r.db.Query(ctx, `
		SELECT r.code 
		FROM kerjantara.mst_user_roles ur
		JOIN kerjantara.ref_user_roles r ON ur.role_id = r.id
		WHERE ur.user_id = $1
	`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		u.Roles = append(u.Roles, role)
	}

	return u, nil
}

func (r *Repository) GetUserByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	var lat, lng sql.NullFloat64

	err := r.db.QueryRow(ctx, `
		SELECT u.id, u.verif_status_id, s.code, u.full_name, u.phone, u.password_hash, 
		       u.ktp_file_key, u.selfie_file_key, u.is_active, 
		       ST_Y(u.location::geometry) as lat, ST_X(u.location::geometry) as lng,
		       u.created_at, u.updated_at
		FROM kerjantara.mst_users u
		JOIN kerjantara.ref_verif_statuses s ON u.verif_status_id = s.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, id).Scan(&u.ID, &u.VerifStatusID, &u.VerifStatus, &u.FullName, &u.Phone, &u.PasswordHash,
		&u.KTPFileKey, &u.SelfieFileKey, &u.IsActive, &lat, &lng, &u.CreatedAt, &u.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if lat.Valid {
		u.Lat = &lat.Float64
	}
	if lng.Valid {
		u.Lng = &lng.Float64
	}

	// Get Roles
	rows, err := r.db.Query(ctx, `
		SELECT r.code 
		FROM kerjantara.mst_user_roles ur
		JOIN kerjantara.ref_user_roles r ON ur.role_id = r.id
		WHERE ur.user_id = $1
	`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		u.Roles = append(u.Roles, role)
	}

	return u, nil
}

func (r *Repository) UpdateKTPKeys(ctx context.Context, userID string, ktpKey, selfieKey string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE kerjantara.mst_users
		SET ktp_file_key = $1, selfie_file_key = $2, updated_at = now()
		WHERE id = $3
	`, ktpKey, selfieKey, userID)
	return err
}

func (r *Repository) UpdateWorkerAvailability(ctx context.Context, userID string, isAvailable bool, lat, lng float64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Update location in mst_users
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.mst_users
		SET location = ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, updated_at = now()
		WHERE id = $3
	`, lng, lat, userID)
	if err != nil {
		return err
	}

	// Update availability in mst_worker_profiles
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.mst_worker_profiles
		SET is_available = $1, updated_at = now()
		WHERE user_id = $2
	`, isAvailable, userID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) AddUserRole(ctx context.Context, userID string, roleCode string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var roleID int
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_user_roles WHERE code = $1", roleCode).Scan(&roleID)
	if err != nil {
		return fmt.Errorf("role code %s not found: %w", roleCode, err)
	}

	// Check if already has role
	var exists bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM kerjantara.mst_user_roles WHERE user_id = $1 AND role_id = $2)", userID, roleID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil // already has role
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO kerjantara.mst_user_roles (user_id, role_id, is_primary)
		VALUES ($1, $2, false)
	`, userID, roleID)
	if err != nil {
		return err
	}

	// If activated role is worker, lazy create profile
	if roleCode == "worker" {
		_, err = tx.Exec(ctx, `
			INSERT INTO kerjantara.mst_worker_profiles (user_id, kerjantara_score, total_jobs_done, avg_response_min, is_available)
			VALUES ($1, 0.00, 0, 0, false)
			ON CONFLICT (user_id) DO NOTHING
		`, userID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (r *Repository) GetWorkerProfile(ctx context.Context, userID string) (*WorkerProfile, error) {
	wp := &WorkerProfile{}
	var bio sql.NullString

	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, years_experience, kerjantara_score, total_jobs_done, 
		       avg_response_min, bio, is_available, kitadompet_balance
		FROM kerjantara.mst_worker_profiles
		WHERE user_id = $1
	`, userID).Scan(&wp.ID, &wp.UserID, &wp.YearsExperience, &wp.KerjantaraScore, &wp.TotalJobsDone,
		&wp.AvgResponseMin, &bio, &wp.IsAvailable, &wp.KitaDompetBalance)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if bio.Valid {
		wp.Bio = &bio.String
	}

	// Get Skills
	rows, err := r.db.Query(ctx, `
		SELECT c.code, c.label_id, ws.is_primary
		FROM kerjantara.mst_worker_skills ws
		JOIN kerjantara.ref_skill_categories c ON ws.skill_cat_id = c.id
		WHERE ws.worker_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sk Skill
		if err := rows.Scan(&sk.Code, &sk.Label, &sk.IsPrimary); err != nil {
			return nil, err
		}
		wp.Skills = append(wp.Skills, sk)
	}

	return wp, nil
}

func (r *Repository) GetPendingVerifications(ctx context.Context) ([]PendingVerification, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.full_name, u.phone, r.code, u.ktp_file_key, u.selfie_file_key, u.created_at
		FROM kerjantara.mst_users u
		JOIN kerjantara.ref_verif_statuses s ON u.verif_status_id = s.id
		JOIN kerjantara.mst_user_roles ur ON ur.user_id = u.id AND ur.is_primary = true
		JOIN kerjantara.ref_user_roles r ON ur.role_id = r.id
		WHERE s.code = 'pending' AND u.ktp_file_key IS NOT NULL AND u.deleted_at IS NULL
		ORDER BY u.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PendingVerification
	for rows.Next() {
		var pv PendingVerification
		var ktpKey, selfieKey string
		err := rows.Scan(&pv.UserID, &pv.FullName, &pv.Phone, &pv.Role, &ktpKey, &selfieKey, &pv.SubmittedAt)
		if err != nil {
			return nil, err
		}
		pv.KTPFileKey = ktpKey
		// URL akan di-generate signed-nya di level service
		result = append(result, pv)
	}

	return result, nil
}

func (r *Repository) ReviewVerification(ctx context.Context, userID string, decision string, note string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var statusID int
	err = tx.QueryRow(ctx, "SELECT id FROM kerjantara.ref_verif_statuses WHERE code = $1", decision).Scan(&statusID)
	if err != nil {
		return fmt.Errorf("status code %s not found: %w", decision, err)
	}

	// Update status
	_, err = tx.Exec(ctx, `
		UPDATE kerjantara.mst_users
		SET verif_status_id = $1, updated_at = now()
		WHERE id = $2
	`, statusID, userID)
	if err != nil {
		return err
	}

	// Jika approved dan user memiliki role 'worker', kita ubah availability-nya atau pastikan profile-nya ada
	// (meski sudah lazy created sebelumnya jika daftar sebagai worker)
	// Catat note jika ditolak/resubmit. Tapi di database schema 'mst_users' tidak ada kolom note verifikasi.
	// Oh, di schema, decision note bisa dikirim via WebSocket atau dicatat di log. Namun, untuk MVP, kita update verif_status saja.

	return tx.Commit(ctx)
}
