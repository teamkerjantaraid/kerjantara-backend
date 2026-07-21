package job

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"kerjantara-backend/pkg/middleware"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service            *Service
	gpsToleranceMeters float64
}

func NewHandler(service *Service, gpsTolerance float64) *Handler {
	return &Handler{
		service:            service,
		gpsToleranceMeters: gpsTolerance,
	}
}

func (h *Handler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	// Reference data lookup (CORS friendly / public)
	r.Get("/ref/rate-cards", h.GetRateCard)
	r.Get("/ref/skill-categories", h.GetSkillCategories)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		r.Post("/jobs", h.CreateJob)
		r.Get("/jobs/{id}", h.GetJob)
		r.Get("/jobs/employer", h.GetJobsForEmployer)
		r.Get("/jobs/worker", h.GetJobsForWorker)
		r.Patch("/jobs/{id}/accept-match", h.AcceptJob)
		r.Patch("/jobs/{id}/reject-match", h.RejectJob)
		r.Patch("/jobs/{id}/arrive", h.ArriveAtJob)
		r.Post("/jobs/{id}/complete", h.CompleteJob) // POST / PATCH are both okay. Let's make it PATCH /jobs/{id}/complete in code, but support it
		r.Patch("/jobs/{id}/complete", h.CompleteJob)
		r.Patch("/jobs/{id}/confirm", h.ConfirmJob)
		r.Post("/jobs/{id}/rate", h.RateJob)
		r.Post("/jobs/{id}/match-fallback", h.MatchJobCityFallback)
		r.Patch("/jobs/{id}/days/{day_number}/complete", h.CompleteDay)
		r.Patch("/jobs/{id}/days/{day_number}/confirm", h.ConfirmDay)
	})
}

// GetRateCard godoc
// @Summary      Ambil rate card pasar untuk kategori skill dan kota
// @Description  Mengembalikan harga min/max pasar untuk kategori skill tertentu di kota tertentu. Endpoint publik, tidak memerlukan autentikasi.
// @Tags         Job
// @Produce      json
// @Param        skill_cat_id  query  integer  true  "ID kategori skill"
// @Param        city_code     query  string   true  "Kode kota (contoh: JKT)"
// @Success      200  {object}  docs.SuccessEnvelope{data=RateCardResponse}
// @Failure      404  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Router       /ref/rate-cards [get]
func (h *Handler) GetRateCard(w http.ResponseWriter, r *http.Request) {
	skillCatIDStr := r.URL.Query().Get("skill_cat_id")
	cityCode := r.URL.Query().Get("city_code")

	if skillCatIDStr == "" || cityCode == "" {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Skill Category ID dan City Code wajib diisi")
		return
	}

	skillCatID, err := strconv.Atoi(skillCatIDStr)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Skill Category ID harus berupa angka")
		return
	}

	rc, err := h.service.GetRateCard(r.Context(), skillCatID, cityCode)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if rc == nil {
		respondWithError(w, http.StatusNotFound, "NOT_FOUND", "Rate card tidak ditemukan untuk kota dan kategori tersebut")
		return
	}

	respondWithJSON(w, http.StatusOK, rc)
}

// GetSkillCategories godoc
// @Summary      Ambil daftar semua kategori skill
// @Description  Mengembalikan seluruh kategori skill yang tersedia di platform. Endpoint publik, tidak memerlukan autentikasi.
// @Tags         Job
// @Produce      json
// @Success      200  {object}  docs.SuccessEnvelope{data=[]map[string]interface{}}
// @Failure      500  {object}  docs.ErrorEnvelope
// @Router       /ref/skill-categories [get]
func (h *Handler) GetSkillCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.service.GetSkillCategories(r.Context())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"categories": cats,
	})
}

// CreateJob godoc
// @Summary      Buat pekerjaan baru
// @Description  Employer membuat permintaan pekerjaan. Sistem akan menjalankan MatchingEngine untuk menemukan maksimal 3 kandidat worker berdasarkan GPS, skor, dan keahlian.
// @Tags         Job
// @Accept       json
// @Produce      json
// @Param        body  body      CreateJobRequest  true  "Data pekerjaan"
// @Success      201   {object}  docs.SuccessEnvelope{data=CreateJobResponse}
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs [post]
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		SkillCatID int     `json:"skill_cat_id"`
		Description string  `json:"description"`
		Budget      int64   `json:"budget"`
		Lat         float64 `json:"lat"`
		Lng         float64 `json:"lng"`
		CityCode    string  `json:"city_code"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	job, candidates, err := h.service.CreateJob(r.Context(), claims.UserID, req.SkillCatID, req.Description, req.Budget, req.Lat, req.Lng, req.CityCode)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	// Fetch Rate Card to provide market reference in response
	rateCard, _ := h.service.GetRateCard(r.Context(), req.SkillCatID, req.CityCode)

	var budgetVsMarket = "within_range"
	var rcResponse interface{} = nil
	if rateCard != nil {
		rcResponse = map[string]interface{}{
			"min_rate":  rateCard.MinRate,
			"max_rate":  rateCard.MaxRate,
			"rate_unit": rateCard.RateUnit,
			"label":     rateCard.Label,
		}

		if req.Budget < rateCard.MinRate {
			budgetVsMarket = "below_range"
		} else if req.Budget > rateCard.MaxRate {
			budgetVsMarket = "above_range"
		}
	}

	// Format candidates matching API Contract
	formattedCandidates := []map[string]interface{}{}
	for i, c := range candidates {
		// Response window is 15 minutes
		deadline := time.Now().Add(15 * time.Minute)
		
		formattedCandidates = append(formattedCandidates, map[string]interface{}{
			"match_id":          c.MatchID,
			"match_rank":        i + 1,
			"worker_id":         c.WorkerID,
			"full_name":         c.FullName,
			"kerjantara_score":   c.KerjantaraScore,
			"total_jobs_done":    c.TotalJobsDone,
			"distance_km":       mathRound(c.DistanceMeters/1000.0, 1),
			"avg_response_min":   c.AvgResponseMin,
			"bio":               c.Bio,
			"composite_score":   mathRound(c.CompositeScore, 2),
			"response_deadline": deadline,
		})
	}

	status := job.Status
	if len(candidates) == 0 && status != "pending_city_fallback" {
		status = "no_candidates"
	}

	respondWithJSON(w, http.StatusCreated, map[string]interface{}{
		"job_id":                  job.ID,
		"status":                  status,
		"budget":                  job.Budget,
		"rate_card":               rcResponse,
		"budget_vs_market":        budgetVsMarket,
		"response_window_minutes": 15,
		"candidates":              formattedCandidates,
	})
}

// MatchJobCityFallback godoc
// @Summary      Fallback matching ke level kota
// @Description  Memperluas pencarian kandidat ke seluruh kota ketika matching radius awal tidak menghasilkan kandidat yang cukup.
// @Tags         Job
// @Accept       json
// @Produce      json
// @Param        id    path  string             true  "Job ID (UUID)"
// @Param        body  body  MatchFallbackRequest  true  "City ID untuk fallback"
// @Success      200   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/match-fallback [post]
func (h *Handler) MatchJobCityFallback(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Job ID diperlukan")
		return
	}

	var req struct {
		CityID int `json:"city_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	candidates, err := h.service.MatchJobCityFallback(r.Context(), jobID, req.CityID)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	formattedCandidates := []map[string]interface{}{}
	for i, c := range candidates {
		deadline := time.Now().Add(15 * time.Minute)
		formattedCandidates = append(formattedCandidates, map[string]interface{}{
			"match_id":          c.MatchID,
			"match_rank":        i + 1,
			"worker_id":         c.WorkerID,
			"full_name":         c.FullName,
			"kerjantara_score":   c.KerjantaraScore,
			"total_jobs_done":    c.TotalJobsDone,
			"distance_km":       mathRound(c.DistanceMeters/1000.0, 1),
			"avg_response_min":   c.AvgResponseMin,
			"bio":               c.Bio,
			"composite_score":   mathRound(c.CompositeScore, 2),
			"response_deadline": deadline,
		})
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":     jobID,
		"status":     "matched",
		"candidates": formattedCandidates,
	})
}

// GetJob godoc
// @Summary      Ambil detail pekerjaan berdasarkan ID
// @Description  Mengembalikan detail lengkap sebuah pekerjaan termasuk status, kandidat, dan informasi pekerja yang diterima.
// @Tags         Job
// @Produce      json
// @Param        id  path  string  true  "Job ID (UUID)"
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      404  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id} [get]
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Job ID diperlukan")
		return
	}

	job, err := h.service.GetJob(r.Context(), jobID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, job)
}

// GetJobsForEmployer godoc
// @Summary      Ambil daftar pekerjaan milik employer
// @Description  Mengembalikan daftar pekerjaan yang dibuat oleh employer yang sedang login, dengan opsi filter status dan paginasi.
// @Tags         Job
// @Produce      json
// @Param        status  query  string  false  "Filter status pekerjaan (contoh: matched, done, cancelled)"
// @Param        page    query  int     false  "Halaman (default: 1)"
// @Param        limit   query  int     false  "Jumlah per halaman (default: 10)"
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      500  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/employer [get]
func (h *Handler) GetJobsForEmployer(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}

	jobs, total, err := h.service.GetJobsForEmployer(r.Context(), claims.UserID, status, page, limit)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":  jobs,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// GetJobsForWorker godoc
// @Summary      Ambil daftar pekerjaan untuk worker
// @Description  Mengembalikan daftar pekerjaan yang terhubung dengan worker yang sedang login, dengan opsi filter status dan paginasi.
// @Tags         Job
// @Produce      json
// @Param        status  query  string  false  "Filter status pekerjaan (contoh: accepted, done)"
// @Param        page    query  int     false  "Halaman (default: 1)"
// @Param        limit   query  int     false  "Jumlah per halaman (default: 10)"
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      500  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/worker [get]
func (h *Handler) GetJobsForWorker(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}

	jobs, total, err := h.service.GetJobsForWorker(r.Context(), claims.UserID, status, page, limit)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":  jobs,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// AcceptJob godoc
// @Summary      Worker menerima tawaran pekerjaan
// @Description  Worker menerima tawaran match dari employer. Hanya satu worker yang bisa menerima per job. Jika job sudah diambil worker lain, akan dikembalikan error 409.
// @Tags         Job
// @Accept       json
// @Produce      json
// @Param        id    path  string            true  "Job ID (UUID)"
// @Param        body  body  AcceptMatchRequest  true  "Match ID yang diterima"
// @Success      200   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      403   {object}  docs.ErrorEnvelope  "KTP_NOT_VERIFIED / WORKER_NOT_AVAILABLE"
// @Failure      409   {object}  docs.ErrorEnvelope  "JOB_TAKEN — job sudah diterima worker lain"
// @Failure      422   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/accept-match [patch]
func (h *Handler) AcceptJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		MatchID string `json:"match_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	job, err := h.service.AcceptJob(r.Context(), jobID, claims.UserID, req.MatchID)
	if err != nil {
		if err.Error() == "JOB_TAKEN" {
			respondWithError(w, http.StatusConflict, "JOB_TAKEN", "Job sudah diterima oleh pekerja lain")
			return
		}
		if err.Error() == "KTP_NOT_VERIFIED" {
			respondWithError(w, http.StatusForbidden, "KTP_NOT_VERIFIED", "verifikasi identitas (KTP) harus disetujui terlebih dahulu")
			return
		}
		if err.Error() == "WORKER_NOT_AVAILABLE" {
			respondWithError(w, http.StatusForbidden, "WORKER_NOT_AVAILABLE", "worker sedang tidak tersedia untuk menerima pekerjaan")
			return
		}
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	// Platform fee logic:
	// < 1.000.000 flat Rp 10.000
	// >= 1.000.000 -> 2%
	var platformFee int64 = 10000
	if *job.AgreedPrice >= 1000000 {
		platformFee = int64(float64(*job.AgreedPrice) * 0.02)
	}
	totalCharged := *job.AgreedPrice + platformFee

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":                     job.ID,
		"match_id":                   req.MatchID,
		"agreed_price":               *job.AgreedPrice,
		"platform_fee":               platformFee,
		"net_to_worker":              *job.AgreedPrice,
		"total_charged_to_employer":  totalCharged,
		"status":                     "accepted",
		"message":                    "Job berhasil diterima. Menunggu pemberi kerja mengamankan dana.",
	})
}

// RejectJob godoc
// @Summary      Worker menolak tawaran pekerjaan
// @Description  Worker menolak match dari employer. Sistem akan otomatis memberitahu kandidat berikutnya jika ada.
// @Tags         Job
// @Accept       json
// @Produce      json
// @Param        id    path  string             true  "Job ID (UUID)"
// @Param        body  body  RejectMatchRequest  true  "Match ID yang ditolak"
// @Success      200   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      404   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/reject-match [patch]
func (h *Handler) RejectJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		MatchID string `json:"match_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	nextCandidateNotified, err := h.service.RejectJob(r.Context(), jobID, claims.UserID, req.MatchID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":                  jobID,
		"match_id":                req.MatchID,
		"message":                 "Job berhasil dilewati",
		"next_candidate_notified": nextCandidateNotified,
	})
}

// ArriveAtJob godoc
// @Summary      Worker menandai kedatangan di lokasi pekerjaan
// @Description  Worker mengirimkan koordinat GPS saat tiba di lokasi. Sistem memverifikasi jarak terhadap lokasi job. Jika terlalu jauh, dikembalikan error 422 dengan kode GPS_TOO_FAR.
// @Tags         Job
// @Accept       json
// @Produce      json
// @Param        id    path  string        true  "Job ID (UUID)"
// @Param        body  body  ArriveRequest  true  "Koordinat GPS saat tiba"
// @Success      200   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope  "GPS_TOO_FAR — lokasi terlalu jauh dari titik pekerjaan"
// @Security     BearerAuth
// @Router       /jobs/{id}/arrive [patch]
func (h *Handler) ArriveAtJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	job, distance, err := h.service.ArriveAtJob(r.Context(), jobID, claims.UserID, req.Lat, req.Lng, h.gpsToleranceMeters)
	if err != nil {
		if err.Error() == "GPS_TOO_FAR" {
			respondWithError(w, http.StatusUnprocessableEntity, "GPS_TOO_FAR", "Kamu masih terlalu jauh dari lokasi pekerjaan ("+strconv.FormatFloat(distance, 'f', 0, 64)+"m). Pastikan kamu sudah berada di lokasi.")
			return
		}
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":               job.ID,
		"arrived_at":           time.Now(),
		"gps_verified":         true,
		"distance_from_job_m": math.Round(distance),
	})
}

// CompleteJob godoc
// @Summary      Tandai pekerjaan selesai — POST (worker)
// @Description  Worker mengunggah bukti foto penyelesaian pekerjaan via POST. Minimal 1 foto wajib diunggah, total maksimal 20MB.
// @Tags         Job
// @Accept       multipart/form-data
// @Produce      json
// @Param        id              path      string  true   "Job ID (UUID)"
// @Param        proof_photos[]  formData  file    true   "Foto bukti pekerjaan (minimal 1)"
// @Param        notes           formData  string  false  "Catatan penyelesaian"
// @Success      200  {object}  docs.SuccessEnvelope{data=CompleteJobResponse}
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/complete [post]

// CompleteJobPatch godoc
// @Summary      Tandai pekerjaan selesai — PATCH (worker)
// @Description  Worker mengunggah bukti foto penyelesaian pekerjaan via PATCH. Minimal 1 foto wajib diunggah, total maksimal 20MB.
// @Tags         Job
// @Accept       multipart/form-data
// @Produce      json
// @Param        id              path      string  true   "Job ID (UUID)"
// @Param        proof_photos[]  formData  file    true   "Foto bukti pekerjaan (minimal 1)"
// @Param        notes           formData  string  false  "Catatan penyelesaian"
// @Success      200  {object}  docs.SuccessEnvelope{data=CompleteJobResponse}
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/complete [patch]
func (h *Handler) CompleteJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	// Parse multipart
	err := r.ParseMultipartForm(20 << 20) // 20MB max memory for multiple files
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ukuran file terlalu besar")
		return
	}

	files := r.MultipartForm.File["proof_photos[]"]
	if len(files) == 0 {
		// API contract specifies proof_photos[] is array
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Minimal 1 foto bukti wajib diunggah")
		return
	}

	var readers []io.Reader
	var sizes []int64
	var types []string

	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Gagal membuka file")
			return
		}
		defer file.Close()

		readers = append(readers, file)
		sizes = append(sizes, fileHeader.Size)
		types = append(types, fileHeader.Header.Get("Content-Type"))
	}

	notes := r.FormValue("notes")

	job, signedURLs, err := h.service.CompleteJob(r.Context(), jobID, claims.UserID, readers, sizes, types, notes)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	// Keys extracted from signed urls or keys from storage path
	var fileKeys []string
	for i := range files {
		ext := getExtensionFromMime(types[i], ".jpg")
		key := fmt.Sprintf("proof/%s/%d%s", jobID, i+1, ext)
		fileKeys = append(fileKeys, key)
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":           job.ID,
		"status":           "done",
		"proof_photo_urls": signedURLs,
		"proof_file_keys":  fileKeys,
		"message":          "Menunggu konfirmasi pemberi kerja",
	})
}

// ConfirmJob godoc
// @Summary      Employer mengkonfirmasi pekerjaan selesai
// @Description  Employer mengkonfirmasi bahwa pekerjaan telah selesai dikerjakan. Dana escrow dilepaskan ke worker setelah konfirmasi.
// @Tags         Job
// @Produce      json
// @Param        id  path  string  true  "Job ID (UUID)"
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/confirm [patch]
func (h *Handler) ConfirmJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	job, err := h.service.ConfirmJob(r.Context(), jobID, claims.UserID)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":         job.ID,
		"status":         "done",
		"payment_status": "released",
		"message":        "Dana berhasil dilepaskan ke pekerja",
	})
}

// CompleteDay godoc
// @Summary      Pekerja upload bukti selesai per hari (multi-day job)
// @Description  Untuk job dengan duration_days > 1. Pekerja upload bukti pekerjaan per hari. Hari terakhir tetap pakai endpoint ini.
// @Tags         Job
// @Accept       multipart/form-data
// @Produce      json
// @Param        id           path      string  true  "Job ID (UUID)"
// @Param        day_number   path      int     true  "Hari ke-berapa (1..duration_days)"
// @Param        proof_photos formData  file    true  "Foto bukti (min 1, max 5, total max 20MB)"
// @Param        notes        formData  string  false "Catatan hari ini"
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/days/{day_number}/complete [patch]
func (h *Handler) CompleteDay(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	dayStr := chi.URLParam(r, "day_number")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	dayNumber, err := strconv.Atoi(dayStr)
	if err != nil || dayNumber < 1 {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "day_number harus berupa angka positif")
		return
	}

	err = r.ParseMultipartForm(20 << 20)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Total file melebihi 20MB")
		return
	}

	files := r.MultipartForm.File["proof_photos"]
	if len(files) == 0 {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Minimal upload 1 foto bukti")
		return
	}
	if len(files) > 5 {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Maksimal 5 file foto bukti")
		return
	}

	var readers []io.Reader
	var sizes []int64
	var types []string

	for _, fh := range files {
		file, err := fh.Open()
		if err != nil {
			respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Gagal membaca file")
			return
		}
		defer file.Close()
		readers = append(readers, file)
		sizes = append(sizes, fh.Size)
		types = append(types, fh.Header.Get("Content-Type"))
	}

	notes := r.FormValue("notes")

	job, signedURLs, err := h.service.CompleteDay(r.Context(), jobID, claims.UserID, dayNumber, readers, sizes, types, notes)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":           job.ID,
		"day_number":       dayNumber,
		"status":           job.Status,
		"proof_photo_urls": signedURLs,
		"message":          fmt.Sprintf("Hari ke-%d selesai. Menunggu konfirmasi pemberi kerja.", dayNumber),
	})
}

// ConfirmDay godoc
// @Summary      Pemberi kerja konfirmasi hari ke-N selesai (multi-day job)
// @Description  Untuk job dengan duration_days > 1. Employer konfirmasi pekerjaan per hari. Konfirmasi hari terakhir otomatis menyelesaikan job.
// @Tags         Job
// @Accept       json
// @Produce      json
// @Param        id          path  string  true  "Job ID (UUID)"
// @Param        day_number  path  int     true  "Hari ke-berapa (1..duration_days)"
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/days/{day_number}/confirm [patch]
func (h *Handler) ConfirmDay(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	dayStr := chi.URLParam(r, "day_number")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	dayNumber, err := strconv.Atoi(dayStr)
	if err != nil || dayNumber < 1 {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "day_number harus berupa angka positif")
		return
	}

	job, err := h.service.ConfirmDay(r.Context(), jobID, claims.UserID, dayNumber)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	isLastDay := dayNumber == job.DurationDays
	msg := fmt.Sprintf("Hari ke-%d berhasil dikonfirmasi.", dayNumber)
	if isLastDay {
		msg = fmt.Sprintf("Hari ke-%d (terakhir) dikonfirmasi. Job selesai. Dana dirilis ke pekerja.", dayNumber)
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"job_id":     job.ID,
		"day_number": dayNumber,
		"status":     job.Status,
		"is_last":    isLastDay,
		"message":    msg,
	})
}

// RateJob godoc
// @Summary      Beri rating untuk pekerjaan yang telah selesai
// @Description  Baik employer maupun worker dapat memberikan rating setelah pekerjaan selesai. Employer menilai worker, worker menilai employer. Skor 1.0–5.0 skala bintang.
// @Tags         Job
// @Accept       json
// @Produce      json
// @Param        id    path  string        true  "Job ID (UUID)"
// @Param        body  body  RateJobRequest  true  "Rating dan komentar"
// @Success      201   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /jobs/{id}/rate [post]
func (h *Handler) RateJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		Score   float64 `json:"score"`
		Comment string  `json:"comment"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	ratingID, err := h.service.RateJob(r.Context(), jobID, claims.UserID, req.Score, req.Comment)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	job, _ := h.service.GetJob(r.Context(), jobID)
	rateeName := ""
	if job != nil {
		if job.EmployerID == claims.UserID && job.AcceptedWorker != nil {
			rateeName = job.AcceptedWorker.FullName
		} else {
			rateeName = job.EmployerName
		}
	}

	respondWithJSON(w, http.StatusCreated, map[string]interface{}{
		"rating_id":  ratingID,
		"ratee_name": rateeName,
		"score":      req.Score,
		"message":    "Rating berhasil disimpan",
	})
}

// Helpers
func respondWithJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data": data,
		"meta": map[string]interface{}{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
}

func respondWithError(w http.ResponseWriter, statusCode int, errorCode string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    errorCode,
			"message": message,
		},
	})
}

func mathRound(val float64, precision int) float64 {
	ratio := math.Pow(10, float64(precision))
	return math.Round(val*ratio) / ratio
}
