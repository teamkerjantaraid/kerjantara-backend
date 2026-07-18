package payment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"kerjantara-backend/internal/job"
	"kerjantara-backend/pkg/event"
)

type Service struct {
	repo              *Repository
	jobRepo           *job.Repository
	midtransServerKey string
}

func NewService(repo *Repository, jobRepo *job.Repository, serverKey string) *Service {
	return &Service{
		repo:              repo,
		jobRepo:           jobRepo,
		midtransServerKey: serverKey,
	}
}

func (s *Service) CreatePayment(ctx context.Context, jobID string) (*Payment, error) {
	// Ambil detail job dari job module
	j, err := s.jobRepo.GetJobByID(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job for payment: %w", err)
	}
	if j == nil {
		return nil, errors.New("job tidak ditemukan")
	}

	if j.AgreedPrice == nil {
		return nil, errors.New("harga pekerjaan belum disepakati")
	}

	amount := *j.AgreedPrice

	// Platform fee logic:
	// Transaksi < 1.000.000 -> flat 10.000
	// Transaksi >= 1.000.000 -> 2% dari agreed_price
	var platformFee int64 = 10000
	if amount >= 1000000 {
		platformFee = int64(float64(amount) * 0.02)
	}
	totalCharged := amount + platformFee
	netToWorker := amount

	// Generate Order ID
	orderID := fmt.Sprintf("KJT-%s-%s", time.Now().Format("20060102"), jobID[:8])

	snapToken := "mock-snap-token-" + jobID[:8]

	// Panggil Midtrans Snap API jika Server Key disediakan
	if s.midtransServerKey != "" && s.midtransServerKey != "SB-Mid-server-xxxx" {
		token, err := s.callMidtransSnap(orderID, totalCharged)
		if err == nil {
			snapToken = token
		} else {
			log.Printf("[Payment Service] warning: failed call Midtrans API, fallback to mock: %v\n", err)
		}
	}

	p := &Payment{
		JobID:             jobID,
		EmployerID:        j.EmployerID,
		WorkerID:          j.AcceptedWorker.UserID,
		Amount:            amount,
		PlatformFee:       platformFee,
		NetToWorker:       netToWorker,
		MidtransOrderID:   orderID,
		MidtransSnapToken: &snapToken,
	}

	err = s.repo.CreatePayment(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("failed to save payment: %w", err)
	}

	// Jika job multi-hari, buat baris milestone harian
	if j.DurationDays > 1 {
		err = s.repo.CreateMilestones(ctx, p.ID, j.DurationDays, amount)
		if err != nil {
			log.Printf("[Payment Service] failed to create daily milestones: %v\n", err)
		}
	}

	return p, nil
}

func (s *Service) callMidtransSnap(orderID string, amount int64) (string, error) {
	url := "https://app.sandbox.midtrans.com/snap/v1/transactions"
	
	payload := map[string]interface{}{
		"transaction_details": map[string]interface{}{
			"order_id":     orderID,
			"gross_amount": amount,
		},
		"credit_card": map[string]interface{}{
			"secure": true,
		},
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", err
	}

	auth := base64.StdEncoding.EncodeToString([]byte(s.midtransServerKey + ":"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+auth)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("midtrans error, status: %d, response: %s", resp.StatusCode, string(bodyBytes))
	}

	var res struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&res)
	if err != nil {
		return "", err
	}

	return res.Token, nil
}

func (s *Service) GetPaymentStatus(ctx context.Context, jobID string) (*Payment, error) {
	return s.repo.GetPaymentByJobID(ctx, jobID)
}

func (s *Service) GetMilestones(ctx context.Context, jobID string) ([]Milestone, error) {
	return s.repo.GetMilestones(ctx, jobID)
}

func (s *Service) HandleWebhook(ctx context.Context, orderID string, transactionStatus string, fraudStatus string, rawPayload string) error {
	var localStatus string

	switch transactionStatus {
	case "capture", "settlement":
		if transactionStatus == "capture" && fraudStatus == "challenge" {
			localStatus = "challenge"
		} else {
			localStatus = "held" // dana masuk ke escrow
		}
	case "deny", "expire", "cancel":
		localStatus = "refunded"
	default:
		localStatus = "pending"
	}

	if localStatus == "challenge" {
		return nil // skip challenge for MVP simplicity
	}

	return s.repo.UpdatePaymentStatus(ctx, orderID, localStatus, rawPayload)
}

func (s *Service) StartEventListener(ctx context.Context, bus *event.EventBus) {
	// 1. Subscribe EventJobCompleted -> release escrow ke worker
	completedChan := bus.Subscribe(event.EventJobCompleted)
	// 2. Subscribe EventJobDayCompleted -> release milestone harian ke worker
	dayCompletedChan := bus.Subscribe(event.EventJobDayCompleted)

	go func() {
		for {
			select {
			case ev := <-completedChan:
				s.handleJobCompleted(ctx, ev)
			case ev := <-dayCompletedChan:
				s.handleJobDayCompleted(ctx, ev)
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Println("Payment Module event listener started successfully")
}

func (s *Service) handleJobCompleted(ctx context.Context, ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	jobID, _ := payload["job_id"].(string)
	if jobID == "" {
		return
	}

	err := s.repo.ReleaseEscrow(ctx, jobID)
	if err != nil {
		log.Printf("[Payment Service] failed to release escrow for job %s: %v\n", jobID, err)
	} else {
		log.Printf("[Payment Service] escrow released successfully for job %s\n", jobID)
		
		// Publish event payment released
		event.GlobalBus.Publish(event.EventPaymentReleased, map[string]interface{}{
			"job_id": jobID,
		})
	}
}

func (s *Service) handleJobDayCompleted(ctx context.Context, ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	jobID, _ := payload["job_id"].(string)
	dayNumberFloat, _ := payload["day_number"].(float64)
	dayNumber := int(dayNumberFloat)

	if jobID == "" || dayNumber <= 0 {
		return
	}

	err := s.repo.ReleaseMilestone(ctx, jobID, dayNumber)
	if err != nil {
		log.Printf("[Payment Service] failed to release milestone for job %s day %d: %v\n", jobID, dayNumber, err)
	} else {
		log.Printf("[Payment Service] milestone released successfully for job %s day %d\n", jobID, dayNumber)
		
		// Publish event payment released (milestone)
		event.GlobalBus.Publish(event.EventPaymentReleased, map[string]interface{}{
			"job_id":     jobID,
			"day_number": dayNumber,
		})
	}
}
