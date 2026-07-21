package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"path/filepath"
	"time"

	"kerjantara-backend/pkg/event"
	"kerjantara-backend/pkg/middleware"
	"kerjantara-backend/pkg/storage"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	repo        *Repository
	jwtSecret   string
	supabaseURL string
}

func NewService(repo *Repository, jwtSecret string, supabaseURL string) *Service {
	return &Service{
		repo:        repo,
		jwtSecret:   jwtSecret,
		supabaseURL: supabaseURL,
	}
}

func (s *Service) Register(ctx context.Context, fullName, phone, password, role string) (*User, string, error) {
	if fullName == "" || phone == "" || password == "" || role == "" {
		return nil, "", errors.New("field registrasi tidak boleh kosong")
	}

	if role != "worker" && role != "employer" {
		return nil, "", errors.New("role tidak valid, harus worker atau employer")
	}

	// Check if phone already registered
	existing, err := s.repo.GetUserByPhone(ctx, phone)
	if err != nil {
		return nil, "", fmt.Errorf("gagal mengecek nomor telepon: %w", err)
	}
	if existing != nil {
		return nil, "", errors.New("nomor telepon sudah terdaftar")
	}

	// Hash password
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("gagal mengenkripsi password: %w", err)
	}

	u := &User{
		FullName:     fullName,
		Phone:        phone,
		PasswordHash: string(hashed),
	}

	createdUser, err := s.repo.CreateUser(ctx, u, role)
	if err != nil {
		return nil, "", err
	}

	// Generate Token
	token, _, err := middleware.GenerateToken(createdUser.ID, createdUser.Roles, role, s.jwtSecret)
	if err != nil {
		return nil, "", fmt.Errorf("gagal membuat token otentikasi: %w", err)
	}

	return createdUser, token, nil
}

func (s *Service) GoogleLogin(ctx context.Context, supabaseAccessToken string) (*User, string, time.Time, error) {
	if supabaseAccessToken == "" {
		return nil, "", time.Time{}, errors.New("access token tidak boleh kosong")
	}

	// Fetch JWKS dari Supabase dan verifikasi token dengan public key ES256
	// JWKS di-cache otomatis oleh jwk.NewCache
	jwksURL := s.supabaseURL + "/auth/v1/.well-known/jwks.json"
	keySet, err := jwk.Fetch(ctx, jwksURL)
	if err != nil {
		log.Printf("[GoogleLogin] gagal fetch JWKS: %v", err)
		return nil, "", time.Time{}, errors.New("gagal memverifikasi token: tidak dapat mengambil kunci publik")
	}

	token, err := jwt.ParseString(supabaseAccessToken,
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
	)
	if err != nil {
		log.Printf("[GoogleLogin] JWT parse error: %v", err)
		return nil, "", time.Time{}, fmt.Errorf("access token Supabase tidak valid: %w", err)
	}

	// Ambil sub (= user ID di Supabase, formatnya UUID)
	sub, ok := token.Subject()
	if !ok || sub == "" {
		return nil, "", time.Time{}, errors.New("sub claim tidak ditemukan di token")
	}

	// Cari user di mst_users berdasarkan id = sub
	log.Printf("[GoogleLogin] sub dari token: %s", sub)
	u, err := s.repo.GetUserBySupabaseID(ctx, sub)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("gagal mencari user: %w", err)
	}
	if u == nil {
		return nil, "", time.Time{}, errors.New("user tidak ditemukan, silakan registrasi terlebih dahulu")
	}

	// Tentukan active role — boleh kosong jika user belum assign role
	activeRole := ""
	for _, r := range u.Roles {
		if r == "worker" {
			activeRole = "worker"
			break
		}
	}
	if activeRole == "" && len(u.Roles) > 0 {
		activeRole = u.Roles[0]
	}

	// Generate JWT app
	appToken, expiresAt, err := middleware.GenerateToken(u.ID, u.Roles, activeRole, s.jwtSecret)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("gagal membuat token: %w", err)
	}

	return u, appToken, expiresAt, nil
}

func (s *Service) Login(ctx context.Context, phone, password string) (*User, string, time.Time, error) {
	if phone == "" || password == "" {
		return nil, "", time.Time{}, errors.New("nomor telepon dan password wajib diisi")
	}

	u, err := s.repo.GetUserByPhone(ctx, phone)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("gagal mengambil data user: %w", err)
	}
	if u == nil {
		return nil, "", time.Time{}, errors.New("nomor telepon atau password salah")
	}

	err = bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	if err != nil {
		return nil, "", time.Time{}, errors.New("nomor telepon atau password salah")
	}

	// Tentukan active role awal. Jika ada worker, jadikan default, kalau tidak employer.
	activeRole := u.Roles[0]
	for _, r := range u.Roles {
		if r == "worker" {
			activeRole = "worker"
			break
		}
	}

	token, expiresAt, err := middleware.GenerateToken(u.ID, u.Roles, activeRole, s.jwtSecret)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("gagal membuat token: %w", err)
	}

	return u, token, expiresAt, nil
}

func (s *Service) UploadKTP(ctx context.Context, userID string, ktpReader, selfieReader io.Reader, ktpSize, selfieSize int64, ktpType, selfieType string) error {
	// Dapatkan extension file dari content-type
	ktpExt := getExtensionFromMime(ktpType, ".jpg")
	selfieExt := getExtensionFromMime(selfieType, ".jpg")

	// Gunakan timestamp agar setiap upload menghasilkan key unik (tidak menimpa file lama)
	ts := time.Now().Unix()
	ktpKey := fmt.Sprintf("ktp/%s_%d%s", userID, ts, ktpExt)
	selfieKey := fmt.Sprintf("selfie/%s_%d%s", userID, ts, selfieExt)

	// Upload KTP ke storage
	if storage.GlobalClient == nil {
		return fmt.Errorf("storage belum dikonfigurasi, upload tidak dapat dilakukan")
	}
	err := storage.GlobalClient.UploadFile(ctx, ktpKey, ktpReader, ktpSize, ktpType)
	if err != nil {
		return fmt.Errorf("gagal mengupload foto KTP: %w", err)
	}

	// Upload Selfie ke storage
	err = storage.GlobalClient.UploadFile(ctx, selfieKey, selfieReader, selfieSize, selfieType)
	if err != nil {
		return fmt.Errorf("gagal mengupload foto selfie: %w", err)
	}

	// Update DB keys
	err = s.repo.UpdateKTPKeys(ctx, userID, ktpKey, selfieKey)
	if err != nil {
		return fmt.Errorf("gagal mengupdate database KTP: %w", err)
	}

	// Publish event KTP Uploaded
	event.GlobalBus.Publish(event.EventKTPUploaded, map[string]interface{}{
		"user_id": userID,
	})

	return nil
}

func (s *Service) GetMe(ctx context.Context, userID string, activeRole string) (*User, error) {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, errors.New("user tidak ditemukan")
	}

	// Ambil Worker Profile jika user memiliki role worker
	hasWorkerRole := false
	for _, r := range u.Roles {
		if r == "worker" {
			hasWorkerRole = true
			break
		}
	}

	if hasWorkerRole {
		wp, err := s.repo.GetWorkerProfile(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("gagal mengambil profil pekerja: %w", err)
		}
		u.WorkerProfile = wp
	}

	// Generate signed URL jika data KTP tersedia
	if storage.GlobalClient != nil {
		if u.KTPFileKey != nil && *u.KTPFileKey != "" {
			signedURL, err := storage.GlobalClient.GetSignedURL(ctx, *u.KTPFileKey, 1*time.Hour)
			if err == nil {
				u.KTPFileKey = &signedURL
			}
		}
		if u.SelfieFileKey != nil && *u.SelfieFileKey != "" {
			signedURL, err := storage.GlobalClient.GetSignedURL(ctx, *u.SelfieFileKey, 1*time.Hour)
			if err == nil {
				u.SelfieFileKey = &signedURL
			}
		}
	}

	return u, nil
}

func (s *Service) ToggleWorkerAvailability(ctx context.Context, userID string, isAvailable bool, lat, lng float64) error {
	return s.repo.UpdateWorkerAvailability(ctx, userID, isAvailable, lat, lng)
}

func (s *Service) ActivateRole(ctx context.Context, userID, role string, skillCatIDs []int16) (string, error) {
	if role != "worker" && role != "employer" {
		return "", errors.New("role tidak valid")
	}

	if role == "worker" && len(skillCatIDs) == 0 {
		return "", errors.New("minimal pilih satu keahlian untuk role pekerja")
	}

	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", errors.New("user tidak ditemukan")
	}

	// Konfirmasi status verifikasi KTP must be approved
	if u.VerifStatus != "approved" {
		return "", errors.New("verifikasi identitas (KTP) harus disetujui terlebih dahulu")
	}

	// Add role
	err = s.repo.AddUserRole(ctx, userID, role, skillCatIDs)
	if err != nil {
		return "", fmt.Errorf("gagal menambahkan role: %w", err)
	}

	// Fetch updated user to generate new token containing updated roles
	updatedUser, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}

	token, _, err := middleware.GenerateToken(userID, updatedUser.Roles, role, s.jwtSecret)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (s *Service) SwitchRole(ctx context.Context, userID, targetRole string) (string, error) {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", errors.New("user tidak ditemukan")
	}

	hasRole := false
	for _, r := range u.Roles {
		if r == targetRole {
			hasRole = true
			break
		}
	}

	if !hasRole {
		return "", fmt.Errorf("user tidak memiliki role %s", targetRole)
	}

	token, _, err := middleware.GenerateToken(userID, u.Roles, targetRole, s.jwtSecret)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (s *Service) GetPendingVerifications(ctx context.Context) ([]PendingVerification, error) {
	pvs, err := s.repo.GetPendingVerifications(ctx)
	if err != nil {
		return nil, err
	}

	for i := range pvs {
		if storage.GlobalClient == nil {
			break
		}
		if pvs[i].KTPFileKey != "" {
			signedURL, err := storage.GlobalClient.GetSignedURL(ctx, pvs[i].KTPFileKey, 1*time.Hour)
			if err == nil {
				pvs[i].KTPPhotoURL = signedURL
			}
		}
		// Selfie file key diasumsikan diubah dari ktp ke selfie di key-nya
		selfieKey := fmt.Sprintf("selfie/%s%s", pvs[i].UserID, filepath.Ext(pvs[i].KTPFileKey))
		signedSelfie, err := storage.GlobalClient.GetSignedURL(ctx, selfieKey, 1*time.Hour)
		if err == nil {
			pvs[i].SelfieURL = signedSelfie
		}
	}

	return pvs, nil
}

func (s *Service) ReviewVerification(ctx context.Context, userID, decision, note string) error {
	if decision != "approved" && decision != "rejected" && decision != "resubmit" {
		return errors.New("keputusan tidak valid, harus approved, rejected, atau resubmit")
	}

	if (decision == "rejected" || decision == "resubmit") && note == "" {
		return errors.New("catatan (note) wajib diisi untuk keputusan penolakan/upload ulang")
	}

	return s.repo.ReviewVerification(ctx, userID, decision, note)
}

func getExtensionFromMime(mimeType, fallback string) string {
	exts, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(exts) == 0 {
		return fallback
	}
	// Mengembalikan ekstensi pertama yang ditemukan (misal: .jpeg atau .jpg)
	return exts[0]
}
