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
