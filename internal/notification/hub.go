package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"kerjantara-backend/pkg/event"
	"kerjantara-backend/pkg/middleware"

	"github.com/gorilla/websocket"
)

type Hub struct {
	clients  map[string][]*websocket.Conn
	mu       sync.RWMutex
	upgrader websocket.Upgrader
}

var GlobalHub = NewHub()

func NewHub() *Hub {
	return &Hub{
		clients: make(map[string][]*websocket.Conn),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				// Izinkan semua origin untuk dev/demo ngrok
				return true
			},
		},
	}
}

func (h *Hub) Register(userID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[userID] = append(h.clients[userID], conn)
	log.Printf("[WS Hub] User %s connected. Total connections: %d\n", userID, len(h.clients[userID]))
}

func (h *Hub) Unregister(userID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conns, exists := h.clients[userID]
	if !exists {
		return
	}

	for i, c := range conns {
		if c == conn {
			// Tutup koneksi
			_ = conn.Close()
			// Hapus dari slice
			h.clients[userID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}

	if len(h.clients[userID]) == 0 {
		delete(h.clients, userID)
	}
	log.Printf("[WS Hub] User %s disconnected.\n", userID)
}

func (h *Hub) Send(userID string, eventType string, payload interface{}) {
	h.mu.RLock()
	conns, exists := h.clients[userID]
	h.mu.RUnlock()

	if !exists || len(conns) == 0 {
		return
	}

	msg := map[string]interface{}{
		"type":      eventType,
		"payload":   payload,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[WS Hub] Gagal marshal ws message: %v\n", err)
		return
	}

	for _, conn := range conns {
		go func(c *websocket.Conn) {
			h.mu.Lock()
			defer h.mu.Unlock()
			// Non-blocking write dengan timeout
			_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err := c.WriteMessage(websocket.TextMessage, jsonBytes)
			if err != nil {
				log.Printf("[WS Hub] Gagal menulis pesan ke koneksi user %s: %v\n", userID, err)
			}
		}(conn)
	}
}

func (h *Hub) StartEventListener(ctx context.Context, bus *event.EventBus) {
	// Subscribe ke semua event yang butuh real-time push
	matchedChan := bus.Subscribe(event.EventJobMatched)
	acceptedChan := bus.Subscribe(event.EventJobAccepted)
	completedChan := bus.Subscribe(event.EventJobCompleted)
	releasedChan := bus.Subscribe(event.EventPaymentReleased)
	ktpChan := bus.Subscribe(event.EventKTPUploaded)
	workerCompletedChan := bus.Subscribe("job.worker_completed")

	go func() {
		for {
			select {
			case ev := <-matchedChan:
				h.handleJobMatched(ev)
			case ev := <-acceptedChan:
				h.handleJobAccepted(ev)
			case ev := <-completedChan:
				h.handleJobCompleted(ev)
			case ev := <-releasedChan:
				h.handlePaymentReleased(ev)
			case ev := <-ktpChan:
				h.handleKTPUploaded(ev)
			case ev := <-workerCompletedChan:
				h.handleWorkerCompleted(ev)
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Println("WS Hub event listener started successfully")
}

func (h *Hub) handleJobMatched(ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	// Cari list candidates
	candidatesVal, exists := payload["candidates"]
	if !exists {
		return
	}

	// Karena candidates dipublish dari matching.Service (tipe slice matching.Candidate),
	// kita lakukan type assertion secara asinkron atau convert ke JSON lalu parse kembali agar modular tanpa import cycle.
	jsonBytes, err := json.Marshal(candidatesVal)
	if err != nil {
		return
	}

	var candidates []struct {
		WorkerID string `json:"worker_id"`
	}
	_ = json.Unmarshal(jsonBytes, &candidates)

	jobID, _ := payload["job_id"].(string)

	// Kirim notifikasi "job.matched" ke setiap kandidat
	for _, c := range candidates {
		h.Send(c.WorkerID, "job.matched", map[string]interface{}{
			"job_id": jobID,
			"message": "Ada pekerjaan baru yang cocok untuk Anda!",
		})
	}
}

func (h *Hub) handleJobAccepted(ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	// JSON bridge untuk mengambil data employer_id & worker name
	jsonBytes, err := json.Marshal(payload["job"])
	if err != nil {
		return
	}

	var jobData struct {
		JobID        string `json:"job_id"`
		EmployerID   string `json:"employer_id"`
		EmployerName string `json:"employer_name"`
		WorkerName   string `json:"worker_name"`
		AcceptedWorker struct {
			FullName string `json:"full_name"`
		} `json:"accepted_worker"`
	}
	_ = json.Unmarshal(jsonBytes, &jobData)

	workerName := jobData.WorkerName
	if jobData.AcceptedWorker.FullName != "" {
		workerName = jobData.AcceptedWorker.FullName
	}

	// Kirim notif ke Employer
	h.Send(jobData.EmployerID, "job.accepted", map[string]interface{}{
		"job_id":      jobData.JobID,
		"worker_name": workerName,
		"message":     fmt.Sprintf("Pekerja %s telah menerima pekerjaan Anda!", workerName),
	})
}

func (h *Hub) handleJobCompleted(ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	employerID, _ := payload["employer_id"].(string)
	workerID, _ := payload["worker_id"].(string)
	jobID, _ := payload["job_id"].(string)

	// Notif ke Employer
	h.Send(employerID, "job.completed", map[string]interface{}{
		"job_id":  jobID,
		"message": "Pekerjaan telah dikonfirmasi selesai.",
	})

	// Notif ke Worker
	h.Send(workerID, "job.completed", map[string]interface{}{
		"job_id":  jobID,
		"message": "Pekerjaan Anda telah disetujui oleh pemberi kerja.",
	})
}

func (h *Hub) handlePaymentReleased(ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	// Perlu kirim notif ke worker bahwa saldo bertambah
	workerID, _ := payload["worker_id"].(string)
	jobID, _ := payload["job_id"].(string)

	if workerID != "" {
		h.Send(workerID, "payment.released", map[string]interface{}{
			"job_id":  jobID,
			"message": "Dana pembayaran telah dirilis ke saldo KitaDompet Anda!",
		})
	}
}

func (h *Hub) handleWorkerCompleted(ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	jobID, _ := payload["job_id"].(string)
	// Cari employer_id. Kita bisa query DB atau pastikan service menyediakannya.
	// Jika tidak disediakan, kita log warning.
	employerID, _ := payload["employer_id"].(string)
	if employerID != "" {
		h.Send(employerID, "job.completed", map[string]interface{}{
			"job_id":  jobID,
			"message": "Pekerja telah menandai pekerjaan selesai. Harap konfirmasi hasil kerja.",
		})
	}
}

func (h *Hub) handleKTPUploaded(ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	userID, _ := payload["user_id"].(string)
	log.Printf("[WS Hub] KTP upload event for user %s — broadcasting to all connected admins\n", userID)

	h.mu.RLock()
	defer h.mu.RUnlock()

	msg := map[string]interface{}{
		"type":      "ktp.uploaded",
		"payload":   ev.Payload,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[WS Hub] Gagal marshal ws message: %v\n", err)
		return
	}

	// Kirim ke semua klien yang terhubung (admin dashboard akan memfilter di sisi FE)
	for userID, conns := range h.clients {
		for _, conn := range conns {
			go func(c *websocket.Conn, uid string) {
				_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := c.WriteMessage(websocket.TextMessage, jsonBytes); err != nil {
					log.Printf("[WS Hub] Gagal mengirim notifikasi KTP ke user %s: %v\n", uid, err)
				}
			}(conn, userID)
		}
	}
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS Hub] Upgrade error: %v\n", err)
		return
	}

	h.Register(claims.UserID, conn)

	// Jalankan ping ticker untuk menjaga koneksi tetap hidup
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
					log.Printf("[WS Hub] Ping gagal ke user %s: %v\n", claims.UserID, err)
					return
				}
			}
		}
	}()

	// Jalankan read pump untuk mendeteksi pemutusan koneksi (ping-pong)
	go func() {
		defer func() {
			h.Unregister(claims.UserID, conn)
		}()

		conn.SetReadLimit(512)
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}
