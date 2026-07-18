package score

import (
	"context"
	"errors"
	"log"
	"math"

	"kerjantara-backend/pkg/event"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetWorkerScore(ctx context.Context, workerID string) (*WorkerScoreData, error) {
	wsd, err := s.repo.GetWorkerScore(ctx, workerID)
	if err != nil {
		return nil, err
	}
	if wsd == nil {
		return nil, errors.New("pekerja tidak ditemukan atau profil belum aktif")
	}

	history, err := s.repo.GetScoreHistory(ctx, workerID)
	if err == nil {
		wsd.History = history
	}

	return wsd, nil
}

func (s *Service) StartEventListener(ctx context.Context, bus *event.EventBus) {
	// Subscribe job.completed
	completedChan := bus.Subscribe(event.EventJobCompleted)
	// Subscribe job.rated
	ratedChan := bus.Subscribe(event.EventJobRated)

	go func() {
		for {
			select {
			case ev := <-completedChan:
				s.handleJobCompleted(ctx, ev)
			case ev := <-ratedChan:
				s.handleJobRated(ctx, ev)
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Println("Score Module event listener started successfully")
}

func (s *Service) handleJobCompleted(ctx context.Context, ev event.Event) {
	// Payload map[string]interface{}
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	workerID, _ := payload["worker_id"].(string)
	if workerID == "" {
		return
	}

	err := s.repo.IncrementTotalJobsDone(ctx, workerID)
	if err != nil {
		log.Printf("[Score Service] gagal increment total jobs done: %v\n", err)
	} else {
		log.Printf("[Score Service] total jobs done incremented for worker %s\n", workerID)
	}
}

func (s *Service) handleJobRated(ctx context.Context, ev event.Event) {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return
	}

	rateeID, _ := payload["ratee_id"].(string)
	jobID, _ := payload["job_id"].(string)
	if rateeID == "" {
		return
	}

	// Cek apakah ratee adalah worker (karena rating bisa ke employer juga, tapi score module hanya peduli worker)
	wsd, err := s.repo.GetWorkerScore(ctx, rateeID)
	if err != nil || wsd == nil {
		// Not a worker profile (or not active yet), skip scoring
		return
	}

	// Dapatkan rating rata-rata
	avgRating, err := s.repo.GetAverageRating(ctx, rateeID)
	if err != nil {
		log.Printf("[Score Service] gagal hitung average rating: %v\n", err)
		return
	}

	oldScore := wsd.KerjantaraScore
	newScore := avgRating

	// Batasi presisi ke 2 desimal
	newScore = mathRound(newScore, 2)
	delta := newScore - oldScore

	err = s.repo.UpdateWorkerScoreAndLog(ctx, rateeID, newScore, delta, jobID)
	if err != nil {
		log.Printf("[Score Service] gagal update worker score: %v\n", err)
	} else {
		log.Printf("[Score Service] worker score updated for %s: old=%f, new=%f, delta=%f\n", rateeID, oldScore, newScore, delta)
	}
}

func mathRound(val float64, precision int) float64 {
	shift := math.Pow(10, float64(precision))
	return math.Round(val*shift) / shift
}
