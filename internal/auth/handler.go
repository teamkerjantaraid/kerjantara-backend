package auth

import (
	"encoding/json"
	"net/http"
	"time"

	"kerjantara-backend/pkg/middleware"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	// Public routes
	r.Post("/auth/register", h.Register)
	r.Post("/auth/login", h.Login)
	r.Post("/auth/google", h.GoogleLogin)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/auth/ktp/upload", h.UploadKTP)
		r.Get("/auth/me", h.GetMe)
		r.Patch("/auth/worker/toggle", h.ToggleWorker)
		r.Post("/auth/roles/activate", h.ActivateRole)
		r.Patch("/auth/roles/switch", h.SwitchRole)
	})

	// Admin routes
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Use(middleware.RequireRole("admin")) // check if user has admin/reviewer role
		r.Get("/admin/ktp/pending", h.GetPendingVerifications)
		r.Patch("/admin/ktp/{user_id}/review", h.ReviewVerification)
	})
}

// Register godoc
// @Summary      Registrasi pengguna baru
// @Description  Membuat akun pengguna baru dengan role worker atau employer. Token JWT langsung dikembalikan setelah registrasi berhasil.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      RegisterRequest  true  "Data registrasi"
// @Success      201   {object}  docs.SuccessEnvelope{data=RegisterResponse}
// @Failure      422   {object}  docs.ErrorEnvelope
// @Router       /auth/register [post]
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FullName string `json:"full_name"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	u, token, err := h.service.Register(r.Context(), req.FullName, req.Phone, req.Password, req.Role)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusCreated, map[string]interface{}{
		"user_id":   u.ID,
		"full_name": u.FullName,
		"role":      req.Role,
		"token":     token,
	})
}

// Login godoc
// @Summary      Login pengguna
// @Description  Autentikasi pengguna menggunakan nomor telepon dan password. Mengembalikan JWT Bearer Token yang berlaku 7 hari.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest  true  "Kredensial login"
// @Success      200   {object}  docs.SuccessEnvelope{data=LoginResponse}
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Router       /auth/login [post]
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	u, token, expiresAt, err := h.service.Login(r.Context(), req.Phone, req.Password)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
		return
	}

	// Active role default
	activeRole := u.Roles[0]
	for _, r := range u.Roles {
		if r == "worker" {
			activeRole = "worker"
			break
		}
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":          u.ID,
		"full_name":        u.FullName,
		"role":             activeRole, // return the active role context
		"verif_status":     u.VerifStatus,
		"token":            token,
		"token_expires_at": expiresAt,
	})
}

// GoogleLogin godoc
// @Summary      Login menggunakan Google via Supabase
// @Description  Menerima access token dari Supabase (hasil login Google), memverifikasi token, lalu mengembalikan JWT app. User harus sudah terdaftar di mst_users dengan ID yang sama dengan sub claim token Supabase.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      GoogleLoginRequest  true  "Access token dari Supabase"
// @Success      200   {object}  docs.SuccessEnvelope{data=LoginResponse}
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Router       /auth/google [post]
func (h *Handler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	u, token, expiresAt, err := h.service.GoogleLogin(r.Context(), req.AccessToken)
	if err != nil {
		status := http.StatusUnauthorized
		code := "UNAUTHORIZED"
		// Jika user tidak ditemukan, kembalikan 404-like message tapi tetap 401 agar tidak leak info
		respondWithError(w, status, code, err.Error())
		return
	}

	activeRole := ""
	for _, role := range u.Roles {
		if role == "worker" {
			activeRole = "worker"
			break
		}
	}
	if activeRole == "" && len(u.Roles) > 0 {
		activeRole = u.Roles[0]
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":          u.ID,
		"full_name":        u.FullName,
		"role":             activeRole,
		"verif_status":     u.VerifStatus,
		"token":            token,
		"token_expires_at": expiresAt,
	})
}

// UploadKTP godoc
// @Summary      Upload foto KTP dan selfie untuk verifikasi identitas
// @Description  Menerima dua file gambar: foto KTP dan foto selfie. Batas ukuran masing-masing 10MB. Setelah upload, status verifikasi menjadi 'pending' hingga direview admin.
// @Tags         Auth
// @Accept       multipart/form-data
// @Produce      json
// @Param        ktp_photo     formData  file  true  "Foto KTP (max 10MB)"
// @Param        selfie_photo  formData  file  true  "Foto selfie dengan KTP (max 10MB)"
// @Success      200  {object}  docs.SuccessEnvelope{data=KTPUploadResponse}
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Failure      500  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /auth/ktp/upload [post]
func (h *Handler) UploadKTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	// Parse multipart form
	err := r.ParseMultipartForm(10 << 20) // 10MB max memory
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ukuran file terlalu besar (max 10MB)")
		return
	}

	ktpPart, ktpHeader, err := r.FormFile("ktp_photo")
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Foto KTP wajib diunggah")
		return
	}
	defer ktpPart.Close()

	selfiePart, selfieHeader, err := r.FormFile("selfie_photo")
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Foto selfie wajib diunggah")
		return
	}
	defer selfiePart.Close()

	ktpType := ktpHeader.Header.Get("Content-Type")
	selfieType := selfieHeader.Header.Get("Content-Type")

	err = h.service.UploadKTP(r.Context(), claims.UserID, ktpPart, selfiePart, ktpHeader.Size, selfieHeader.Size, ktpType, selfieType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"verif_status": "pending",
		"message":      "KTP berhasil diupload, menunggu review",
	})
}

// GetMe godoc
// @Summary      Ambil profil pengguna saat ini
// @Description  Mengembalikan data profil lengkap pengguna yang sedang login berdasarkan JWT token.
// @Tags         Auth
// @Produce      json
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      500  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /auth/me [get]
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	u, err := h.service.GetMe(r.Context(), claims.UserID, claims.ActiveRole)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, u)
}

// ToggleWorker godoc
// @Summary      Toggle status aktif worker
// @Description  Mengubah status ketersediaan worker (aktif/tidak aktif) beserta koordinat GPS terkini.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      ToggleWorkerRequest  true  "Status dan koordinat GPS"
// @Success      200   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Failure      500   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /auth/worker/toggle [patch]
func (h *Handler) ToggleWorker(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		IsActive bool    `json:"is_active"`
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	err := h.service.ToggleWorkerAvailability(r.Context(), claims.UserID, req.IsActive, req.Lat, req.Lng)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"is_active": req.IsActive,
		"message":   "Status aktif berhasil diubah",
	})
}

// ActivateRole godoc
// @Summary      Aktifkan role baru untuk pengguna
// @Description  Menambahkan role baru (worker atau employer) ke akun pengguna. Mengembalikan token JWT baru yang mencerminkan role aktif.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      ActivateRoleRequest  true  "Role yang ingin diaktifkan"
// @Success      200   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /auth/roles/activate [post]
func (h *Handler) ActivateRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		Role        string  `json:"role"`
		SkillCatIDs []int16 `json:"skill_cat_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	token, err := h.service.ActivateRole(r.Context(), claims.UserID, req.Role, req.SkillCatIDs)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"role":    req.Role,
		"token":   token,
		"message": "Role berhasil diaktifkan",
	})
}

// SwitchRole godoc
// @Summary      Ganti role aktif pengguna
// @Description  Mengganti konteks role aktif pengguna. Pengguna harus sudah mengaktifkan role tersebut sebelumnya. Mengembalikan token JWT baru.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      SwitchRoleRequest  true  "Role yang ingin diaktifkan"
// @Success      200   {object}  docs.SuccessEnvelope
// @Failure      401   {object}  docs.ErrorEnvelope
// @Failure      422   {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /auth/roles/switch [patch]
func (h *Handler) SwitchRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token tidak valid")
		return
	}

	var req struct {
		Role string `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	token, err := h.service.SwitchRole(r.Context(), claims.UserID, req.Role)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"role":    req.Role,
		"token":   token,
		"message": "Role berhasil diganti",
	})
}

// GetPendingVerifications godoc
// @Summary      Ambil daftar verifikasi KTP yang menunggu review
// @Description  Mengembalikan semua pengajuan verifikasi KTP dengan status 'pending'. Endpoint ini memerlukan role admin.
// @Tags         Auth
// @Produce      json
// @Success      200  {object}  docs.SuccessEnvelope
// @Failure      401  {object}  docs.ErrorEnvelope
// @Failure      500  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /admin/ktp/pending [get]
func (h *Handler) GetPendingVerifications(w http.ResponseWriter, r *http.Request) {
	pvs, err := h.service.GetPendingVerifications(r.Context())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"submissions": pvs,
		"total":       len(pvs),
	})
}

// ReviewVerification godoc
// @Summary      Review pengajuan verifikasi KTP pengguna
// @Description  Admin menyetujui, menolak, atau meminta pengiriman ulang dokumen KTP pengguna. Endpoint ini memerlukan role admin.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        user_id  path      string                    true  "ID pengguna (UUID)"
// @Param        body     body      ReviewVerificationRequest  true  "Keputusan review"
// @Success      200      {object}  docs.SuccessEnvelope
// @Failure      401      {object}  docs.ErrorEnvelope
// @Failure      422      {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /admin/ktp/{user_id}/review [patch]
func (h *Handler) ReviewVerification(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "User ID diperlukan")
		return
	}

	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	err := h.service.ReviewVerification(r.Context(), userID, req.Decision, req.Note)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	// Fetch user details to respond
	// We can directly mock or scan the info. For simplicity, we just fetch from DB or hardcode the basic response.
	// API contract: return user_id, full_name, verif_status, message
	u, _ := h.service.repo.GetUserByID(r.Context(), userID)
	fullName := ""
	verifStatus := req.Decision
	if u != nil {
		fullName = u.FullName
		verifStatus = u.VerifStatus
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":      userID,
		"full_name":    fullName,
		"verif_status": verifStatus,
		"message":      "KTP berhasil direview",
	})
}

// Helpers for Response
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
