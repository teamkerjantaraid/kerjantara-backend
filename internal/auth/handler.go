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

func (h *Handler) ActivateRole(w http.ResponseWriter, r *http.Request) {
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

	token, err := h.service.ActivateRole(r.Context(), claims.UserID, req.Role)
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
