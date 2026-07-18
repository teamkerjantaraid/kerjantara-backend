package notification

import (
	"net/http"

	_ "kerjantara-backend/internal/docs"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	hub *Hub
}

func NewHandler(hub *Hub) *Handler {
	return &Handler{hub: hub}
}

func (h *Handler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/ws", h.HandleWebSocket)
	})
}

// HandleWebSocket godoc
// @Summary      Koneksi WebSocket untuk notifikasi real-time
// @Description  Upgrade HTTP GET ke protokol WebSocket. JWT dikirim via query parameter ?token=<jwt> karena header Authorization tidak didukung saat WebSocket upgrade. Setelah koneksi berhasil, server mengirim pesan JSON: {"type":"<event>","payload":{...},"timestamp":"<RFC3339>"}. Event types: job.matched (notif ke worker saat job cocok), job.accepted (notif ke employer saat worker menerima), job.completed (notif dari worker ke employer untuk konfirmasi), job.completed (notif konfirmasi selesai), payment.released (notif ke worker saat pembayaran dilepas), ktp.uploaded (broadcast ke admin saat KTP diunggah). Koneksi otomatis ditutup jika token tidak valid (HTTP 401 sebelum upgrade).
// @Tags         Notification
// @Produce      json
// @Param        token  query  string  true  "JWT Bearer Token"
// @Success      101  {string}  string  "Switching Protocols — WebSocket connection established"
// @Failure      401  {object}  docs.ErrorEnvelope
// @Security     BearerAuth
// @Router       /ws [get]
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	h.hub.HandleWebSocket(w, r)
}
