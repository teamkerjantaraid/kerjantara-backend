package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"time"

	"kerjantara-backend/internal/matching"
	"kerjantara-backend/pkg/event"
	"kerjantara-backend/pkg/storage"
)

type Service struct {
	repo            *Repository
	matchingService *matching.Service
}

func NewService(repo *Repository, matchingService *matching.Service) *Service {
	return &Service{
		repo:            repo,
		matchingService: matchingService,
	}
}

func (s *Service) CreateJob(ctx context.Context, employerID string, skillCatID int, description string, budget int64, lat, lng float64, cityCode string) (*Job, []matching.Candidate, error) {
	if description == "" || budget <= 0 || lat == 0 || lng == 0 || cityCode == "" {
		return nil, nil, errors.New("input job tidak lengkap atau tidak valid")
	}

	j := &Job{
		EmployerID:   employerID,
		SkillCatID:   skillCatID,
		Description:  description,
		Budget:       budget,
		Lat:          lat,
		Lng:          lng,
		CityCode:     cityCode,
	}

	// 1. Simpan Job ke database (status awal 'pending')
	job, err := s.repo.CreateJob(ctx, j)
	if err != nil {
		return nil, nil, err
	}

	// 2. Jalankan Matching Engine secara synchronous
	candidates, err := s.matchingService.MatchJob(ctx, job.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("matching engine error: %w", err)
	}

	// Reload detail job terbaru (status mungkin sudah berubah ke 'matched' atau 'pending_city_fallback')
	updatedJob, err := s.repo.GetJobByID(ctx, job.ID)
	if err != nil {
		return job, candidates, nil
	}

	return updatedJob, candidates, nil
}

func (s *Service) MatchJobCityFallback(ctx context.Context, jobID string, cityID int) ([]matching.Candidate, error) {
	return s.matchingService.MatchJobCityFallback(ctx, jobID, cityID)
}

func (s *Service) GetJob(ctx context.Context, jobID string) (*Job, error) {
	j, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, errors.New("job tidak ditemukan")
	}
	return j, nil
}

func (s *Service) GetJobsForEmployer(ctx context.Context, employerID string, status string, page, limit int) ([]Job, int, error) {
	offset := (page - 1) * limit
	return s.repo.GetJobsByEmployer(ctx, employerID, status, limit, offset)
}

func (s *Service) GetJobsForWorker(ctx context.Context, workerID string, status string, page, limit int) ([]Job, int, error) {
	offset := (page - 1) * limit
	return s.repo.GetJobsByWorker(ctx, workerID, status, limit, offset)
}

func (s *Service) AcceptJob(ctx context.Context, jobID string, workerID string, matchID string) (*Job, error) {
	v, err := s.repo.GetWorkerVerification(ctx, workerID)
	if err != nil {
		return nil, fmt.Errorf("gagal verifikasi worker: %w", err)
	}
	if v == nil {
		return nil, errors.New("WORKER_NOT_FOUND")
	}
	if v.VerifStatus != "approved" {
		return nil, errors.New("KTP_NOT_VERIFIED")
	}
	if !v.IsAvailable {
		return nil, errors.New("WORKER_NOT_AVAILABLE")
	}

	// transactional accept dengan row lock
	job, err := s.repo.AcceptJobMatch(ctx, jobID, workerID, matchID)
	if err != nil {
		return nil, err
	}

	// Publish event job.accepted
	event.GlobalBus.Publish(event.EventJobAccepted, map[string]interface{}{
		"job_id":    jobID,
		"worker_id": workerID,
		"job":       job,
	})

	return job, nil
}

func (s *Service) RejectJob(ctx context.Context, jobID string, workerID string, matchID string) (bool, error) {
	nextCandidateNotified, err := s.repo.RejectJobMatch(ctx, jobID, workerID, matchID)
	if err != nil {
		return false, err
	}

	// Publish event job.rejected
	event.GlobalBus.Publish(event.EventJobRejected, map[string]interface{}{
		"job_id":                  jobID,
		"worker_id":               workerID,
		"next_candidate_notified": nextCandidateNotified,
	})

	return nextCandidateNotified, nil
}

func (s *Service) ArriveAtJob(ctx context.Context, jobID string, workerID string, lat, lng float64, gpsToleranceMeters float64) (*Job, float64, error) {
	job, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, 0, err
	}
	if job == nil {
		return nil, 0, errors.New("job tidak ditemukan")
	}

	if job.AcceptedWorker == nil || job.AcceptedWorker.UserID != workerID {
		return nil, 0, errors.New("anda bukan pekerja yang diterima untuk pekerjaan ini")
	}

	// Hitung jarak Haversine
	distance := haversine(lat, lng, job.Lat, job.Lng)
	if distance > gpsToleranceMeters {
		return nil, distance, errors.New("GPS_TOO_FAR")
	}

	// Update status ke 'ongoing'
	err = s.repo.UpdateJobStatusAndLog(ctx, jobID, "ongoing", workerID)
	if err != nil {
		return nil, 0, err
	}

	// Reload
	updatedJob, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, 0, err
	}

	// Publish event
	event.GlobalBus.Publish(event.EventJobAccepted, map[string]interface{}{
		"job_id":     jobID,
		"status":     "ongoing",
		"arrived_at": time.Now(),
	})

	return updatedJob, distance, nil
}

func (s *Service) CompleteJob(ctx context.Context, jobID string, workerID string, proofReaders []io.Reader, proofSizes []int64, proofTypes []string, notes string) (*Job, []string, error) {
	job, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	if job == nil {
		return nil, nil, errors.New("job tidak ditemukan")
	}

	if job.AcceptedWorker == nil || job.AcceptedWorker.UserID != workerID {
		return nil, nil, errors.New("anda bukan pekerja yang terdaftar untuk job ini")
	}

	// Upload file bukti kerja ke storage
	var fileKeys []string
	var signedURLs []string

	if storage.GlobalClient == nil {
		return nil, nil, fmt.Errorf("storage belum dikonfigurasi, upload bukti tidak dapat dilakukan")
	}

	for i, reader := range proofReaders {
		ext := getExtensionFromMime(proofTypes[i], ".jpg")
		key := fmt.Sprintf("proof/%s/%d%s", jobID, i+1, ext)

		err = storage.GlobalClient.UploadFile(ctx, key, reader, proofSizes[i], proofTypes[i])
		if err != nil {
			return nil, nil, fmt.Errorf("gagal mengupload bukti ke-%d: %w", i+1, err)
		}
		fileKeys = append(fileKeys, key)

		signedURL, err := storage.GlobalClient.GetSignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			signedURLs = append(signedURLs, signedURL)
		} else {
			signedURLs = append(signedURLs, "")
		}
	}

	// Save proof ke DB + update status ke 'done'
	err = s.repo.SaveJobProof(ctx, jobID, fileKeys, notes)
	if err != nil {
		return nil, nil, err
	}

	// Reload
	updatedJob, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}

	// Publish event `job.completed`? Tunggu, event `job.completed` di Section 5.2 dipicu saat employer konfirmasi.
	// Saat pekerja complete, kita publish event custom untuk notifikasi ke employer.
	event.GlobalBus.Publish("job.worker_completed", map[string]interface{}{
		"job_id":           jobID,
		"proof_file_keys":  fileKeys,
		"proof_photo_urls": signedURLs,
	})

	return updatedJob, signedURLs, nil
}

func (s *Service) ConfirmJob(ctx context.Context, jobID string, employerID string) (*Job, error) {
	job, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, errors.New("job tidak ditemukan")
	}

	if job.EmployerID != employerID {
		return nil, errors.New("anda bukan pemberi kerja untuk job ini")
	}

	// Update status ke 'done' (untuk kepastian jika belum diset)
	err = s.repo.UpdateJobStatusAndLog(ctx, jobID, "done", employerID)
	if err != nil {
		return nil, err
	}

	// Reload
	updatedJob, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	// Publish event `job.completed` -> memicu payment release & score update
	event.GlobalBus.Publish(event.EventJobCompleted, map[string]interface{}{
		"job_id":      jobID,
		"employer_id": employerID,
		"worker_id":   job.AcceptedWorker.UserID,
		"amount":      *job.AgreedPrice,
	})

	return updatedJob, nil
}

func (s *Service) CompleteDay(ctx context.Context, jobID string, workerID string, dayNumber int, proofReaders []io.Reader, proofSizes []int64, proofTypes []string, notes string) (*Job, []string, error) {
	job, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	if job == nil {
		return nil, nil, errors.New("job tidak ditemukan")
	}
	if job.AcceptedWorker == nil || job.AcceptedWorker.UserID != workerID {
		return nil, nil, errors.New("anda bukan pekerja yang terdaftar untuk job ini")
	}
	if job.DurationDays < 2 {
		return nil, nil, errors.New("job ini single-day — gunakan endpoint /jobs/{id}/complete")
	}
	if dayNumber < 1 || dayNumber > job.DurationDays {
		return nil, nil, fmt.Errorf("hari ke-%d tidak valid, job ini hanya %d hari", dayNumber, job.DurationDays)
	}

	existing, _ := s.repo.GetDayLog(ctx, jobID, dayNumber)
	if existing != nil && existing.ConfirmedBy != nil {
		return nil, nil, errors.New("hari ini sudah dikonfirmasi dan tidak bisa diubah")
	}

	var fileKeys []string
	var signedURLs []string

	if storage.GlobalClient == nil {
		return nil, nil, fmt.Errorf("storage belum dikonfigurasi")
	}

	for i, reader := range proofReaders {
		ext := getExtensionFromMime(proofTypes[i], ".jpg")
		key := fmt.Sprintf("proof/%s/day%d_%d%s", jobID, dayNumber, i+1, ext)

		err = storage.GlobalClient.UploadFile(ctx, key, reader, proofSizes[i], proofTypes[i])
		if err != nil {
			return nil, nil, fmt.Errorf("gagal mengupload bukti ke-%d: %w", i+1, err)
		}
		fileKeys = append(fileKeys, key)

		signedURL, err := storage.GlobalClient.GetSignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			signedURLs = append(signedURLs, signedURL)
		} else {
			signedURLs = append(signedURLs, "")
		}
	}

	err = s.repo.SaveDayProof(ctx, jobID, dayNumber, fileKeys, notes)
	if err != nil {
		return nil, nil, err
	}

	updatedJob, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}

	event.GlobalBus.Publish("job.day_submitted", map[string]interface{}{
		"job_id":           jobID,
		"day_number":       dayNumber,
		"proof_file_keys":  fileKeys,
		"proof_photo_urls": signedURLs,
	})

	return updatedJob, signedURLs, nil
}

func (s *Service) ConfirmDay(ctx context.Context, jobID string, employerID string, dayNumber int) (*Job, error) {
	job, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, errors.New("job tidak ditemukan")
	}
	if job.EmployerID != employerID {
		return nil, errors.New("anda bukan pemberi kerja untuk job ini")
	}
	if job.DurationDays < 2 {
		return nil, errors.New("job ini single-day — gunakan endpoint /jobs/{id}/confirm")
	}
	if dayNumber < 1 || dayNumber > job.DurationDays {
		return nil, fmt.Errorf("hari ke-%d tidak valid, job ini hanya %d hari", dayNumber, job.DurationDays)
	}

	dayLog, _ := s.repo.GetDayLog(ctx, jobID, dayNumber)
	if dayLog == nil || dayLog.CompletedAt.IsZero() {
		return nil, errors.New("hari ini belum diselesaikan oleh pekerja — tidak bisa dikonfirmasi")
	}
	if dayLog.ConfirmedBy != nil {
		return nil, errors.New("hari ini sudah dikonfirmasi sebelumnya")
	}

	err = s.repo.ConfirmDayLog(ctx, jobID, dayNumber, employerID)
	if err != nil {
		return nil, err
	}

	isLastDay := dayNumber == job.DurationDays
	if isLastDay {
		err = s.repo.UpdateJobStatusAndLog(ctx, jobID, "done", employerID)
		if err != nil {
			return nil, err
		}
	}

	event.GlobalBus.Publish(event.EventJobDayCompleted, map[string]interface{}{
		"job_id":     jobID,
		"day_number": dayNumber,
	})

	if isLastDay {
		event.GlobalBus.Publish(event.EventJobCompleted, map[string]interface{}{
			"job_id":      jobID,
			"employer_id": employerID,
			"worker_id":   job.AcceptedWorker.UserID,
			"amount":      *job.AgreedPrice,
		})
	}

	updatedJob, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return updatedJob, nil
}

func (s *Service) RateJob(ctx context.Context, jobID string, raterID string, score float64, comment string) (string, error) {
	job, err := s.repo.GetJobByID(ctx, jobID)
	if err != nil {
		return "", err
	}
	if job == nil {
		return "", errors.New("job tidak ditemukan")
	}

	// Validasi rater
	var rateeID string
	if job.EmployerID == raterID {
		if job.AcceptedWorker == nil {
			return "", errors.New("pekerja belum diterima atau tidak ditemukan")
		}
		rateeID = job.AcceptedWorker.UserID
	} else if job.AcceptedWorker != nil && job.AcceptedWorker.UserID == raterID {
		rateeID = job.EmployerID
	} else {
		return "", errors.New("anda bukan rater yang valid untuk job ini")
	}

	ratingID, err := s.repo.SaveRating(ctx, jobID, raterID, rateeID, score, comment)
	if err != nil {
		return "", err
	}

	// Publish event `job.rated` -> memicu rekalkulasi score
	event.GlobalBus.Publish(event.EventJobRated, map[string]interface{}{
		"job_id":    jobID,
		"rater_id":  raterID,
		"ratee_id":  rateeID,
		"score":     score,
	})

	return ratingID, nil
}

func (s *Service) GetRateCard(ctx context.Context, skillCatID int, cityCode string) (*RateCard, error) {
	return s.repo.GetRateCard(ctx, skillCatID, cityCode)
}

func (s *Service) GetSkillCategories(ctx context.Context) ([]SkillCategory, error) {
	return s.repo.GetSkillCategories(ctx)
}

// Haversine formula
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0 // Radius bumi dalam meter
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

func getExtensionFromMime(mimeType, fallback string) string {
	exts, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(exts) == 0 {
		return fallback
	}
	return exts[0]
}
