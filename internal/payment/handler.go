package payment

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	// Midtrans Webhook (Public, no JWT verification)
	r.Post("/payments/webhook", h.HandleWebhook)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/payments/create", h.CreatePayment)
		r.Get("/payments/{job_id}", h.GetPaymentStatus)
		r.Get("/payments/{job_id}/milestones", h.GetMilestones)
	})
}

func (h *Handler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JobID string `json:"job_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	p, err := h.service.CreatePayment(r.Context(), req.JobID)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}

	// Hitung fee note
	feeNote := "Biaya layanan Rp 10.000 (transaksi di bawah Rp 1.000.000)"
	if p.Amount >= 1000000 {
		feeNote = "Biaya layanan 2% (transaksi Rp 1.000.000 ke atas)"
	}

	respondWithJSON(w, http.StatusCreated, map[string]interface{}{
		"payment_id":                p.ID,
		"snap_token":                p.MidtransSnapToken,
		"agreed_price":              p.Amount,
		"platform_fee":              p.PlatformFee,
		"net_to_worker":             p.NetToWorker,
		"total_charged_to_employer": p.Amount,
		"midtrans_order_id":         p.MidtransOrderID,
		"fee_note":                  feeNote,
	})
}

func (h *Handler) GetPaymentStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "job_id")
	if jobID == "" {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Job ID diperlukan")
		return
	}

	p, err := h.service.GetPaymentStatus(r.Context(), jobID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if p == nil {
		respondWithError(w, http.StatusNotFound, "NOT_FOUND", "Pembayaran untuk job ini belum dibuat")
		return
	}

	respondWithJSON(w, http.StatusOK, p)
}

func (h *Handler) GetMilestones(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "job_id")
	if jobID == "" {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Job ID diperlukan")
		return
	}

	milestones, err := h.service.GetMilestones(r.Context(), jobID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, milestones)
}

func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrderID           string `json:"order_id"`
		TransactionStatus string `json:"transaction_status"`
		FraudStatus       string `json:"fraud_status"`
	}

	// Baca body untuk log raw
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "BAD_REQUEST", "Gagal membaca body")
		return
	}

	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Body request tidak valid")
		return
	}

	err = h.service.HandleWebhook(r.Context(), req.OrderID, req.TransactionStatus, req.FraudStatus, string(bodyBytes))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"message": "OK",
	})
}

// Helpers
func respondWithJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data": data,
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
