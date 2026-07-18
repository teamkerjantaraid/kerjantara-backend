package score

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/workers/{id}/score", h.GetWorkerScore)
}

// GetWorkerScore godoc
// @Summary      Lihat skor reputasi pekerja
// @Description  Mengembalikan KerjantaraScore (rata-rata rating 0.00–5.00) beserta riwayat perubahan skor. kerjantara_score dihitung sebagai rata-rata dari semua rating yang diterima pekerja dari tabel trx_ratings, diperbarui otomatis saat event job.rated diterima. Endpoint ini bersifat publik dan tidak memerlukan autentikasi.
// @Tags         Score
// @Produce      json
// @Param        id   path      string  true  "Worker ID (UUID)"
// @Success      200  {object}  docs.SuccessEnvelope{data=WorkerScoreData}
// @Failure      404  {object}  docs.ErrorEnvelope
// @Failure      422  {object}  docs.ErrorEnvelope
// @Router       /workers/{id}/score [get]
func (h *Handler) GetWorkerScore(w http.ResponseWriter, r *http.Request) {
	workerID := chi.URLParam(r, "id")
	if workerID == "" {
		respondWithError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Worker ID diperlukan")
		return
	}

	wsd, err := h.service.GetWorkerScore(r.Context(), workerID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, wsd)
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
