package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"kerjantara-backend/pkg/event"
)

type Service struct {
	repo                  *Repository
	responseWindowMinutes int
}

func NewService(repo *Repository, responseWindowMinutes int) *Service {
	return &Service{
		repo:                  repo,
		responseWindowMinutes: responseWindowMinutes,
	}
}

func (s *Service) MatchJob(ctx context.Context, jobID string) ([]Candidate, error) {
	job, err := s.repo.GetJobDetails(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job details for matching: %w", err)
	}

	var candidates []Candidate
	currentRadius := 2000.0 // 2 km
	const maxRadius = 10000.0 // 10 km
	const radiusIncrement = 2000.0

	// Loop auto-expand radius
	for {
		candidates, err = s.repo.GetCandidatesForJob(ctx, job, currentRadius)
		if err != nil {
			return nil, fmt.Errorf("failed searching candidates: %w", err)
		}

		// Jika kandidat >= 3 atau radius sudah mencapai max
		if len(candidates) >= 3 || currentRadius >= maxRadius {
			break
		}
		currentRadius += radiusIncrement
	}

	// Jika kandidat < 3, tawarkan fallback kota terdekat
	if len(candidates) < 3 {
		err = s.repo.UpdateJobStatus(ctx, jobID, "pending_city_fallback")
		if err != nil {
			return nil, fmt.Errorf("failed updating job status to city fallback: %w", err)
		}
		return nil, nil // return nil candidates to indicate fallback is offered
	}

	// Lakukan Weighted Scoring
	matches := s.calculateScores(candidates, jobID, currentRadius)

	// Urutkan berdasarkan composite score DESC
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CompositeScore > matches[j].CompositeScore
	})

	// Ambil TOP 3
	if len(matches) > 3 {
		matches = matches[:3]
	}

	// Update match rank & deadline
	now := time.Now()
	// Deadline bergantian per 15 menit per rank?
	// Di API contract: "response_deadline: 15 menit dari sekarang".
	// Jika rank 1 tidak merespons -> notif rank 2, dst. Untuk MVP, deadline awal diset 15 menit untuk ketiga kandidat.
	deadline := now.Add(time.Duration(s.responseWindowMinutes) * time.Minute)

	for i := range matches {
		matches[i].Rank = i + 1
		matches[i].Deadline = deadline
	}

	// Simpan ke DB dan dapatkan match IDs
	savedMatches, err := s.repo.SaveJobMatches(ctx, matches)
	if err != nil {
		return nil, fmt.Errorf("failed to save job matches: %w", err)
	}

	// Update status job ke 'matched'
	err = s.repo.UpdateJobStatus(ctx, jobID, "matched")
	if err != nil {
		return nil, fmt.Errorf("failed to update job status: %w", err)
	}

	// Build maps: workerID → compositeScore, workerID → matchID
	scoreMap := make(map[string]float64)
	matchIDMap := make(map[string]string)
	for _, m := range savedMatches {
		scoreMap[m.WorkerID] = m.CompositeScore
		matchIDMap[m.WorkerID] = m.ID
	}

	// Ambil kandidat terpilih saja untuk dikembalikan
	var matchedCandidates []Candidate
	for i := range candidates {
		if score, ok := scoreMap[candidates[i].WorkerID]; ok {
			candidates[i].CompositeScore = score
			candidates[i].MatchID = matchIDMap[candidates[i].WorkerID]
			matchedCandidates = append(matchedCandidates, candidates[i])
		}
	}

	// Urutkan matchedCandidates berdasarkan composite score DESC
	sort.Slice(matchedCandidates, func(i, j int) bool {
		return matchedCandidates[i].CompositeScore > matchedCandidates[j].CompositeScore
	})

	// Publish Event
	event.GlobalBus.Publish(event.EventJobMatched, map[string]interface{}{
		"job_id":     jobID,
		"candidates": matchedCandidates,
	})

	return matchedCandidates, nil
}

func (s *Service) MatchJobCityFallback(ctx context.Context, jobID string, cityID int) ([]Candidate, error) {
	job, err := s.repo.GetJobDetails(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job details: %w", err)
	}

	candidates, err := s.repo.GetCandidatesForJobByCity(ctx, job, cityID)
	if err != nil {
		return nil, fmt.Errorf("failed searching candidates by city: %w", err)
	}

	if len(candidates) == 0 {
		// Update status job ke 'expired' jika benar-benar tidak ada
		err = s.repo.UpdateJobStatus(ctx, jobID, "expired")
		if err != nil {
			return nil, fmt.Errorf("failed to expire job: %w", err)
		}
		return nil, errors.New("tidak ada kandidat ditemukan di kota terpilih")
	}

	// Lakukan scoring dengan radius scale 50km (karena cakupan kota bisa besar)
	matches := s.calculateScores(candidates, jobID, 50000.0)

	// Sort DESC
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CompositeScore > matches[j].CompositeScore
	})

	// Ambil TOP 3
	if len(matches) > 3 {
		matches = matches[:3]
	}

	now := time.Now()
	deadline := now.Add(time.Duration(s.responseWindowMinutes) * time.Minute)

	for i := range matches {
		matches[i].Rank = i + 1
		matches[i].Deadline = deadline
	}

	// Simpan ke DB dan dapatkan match IDs
	savedMatches, err := s.repo.SaveJobMatches(ctx, matches)
	if err != nil {
		return nil, fmt.Errorf("failed to save job matches: %w", err)
	}

	// Update status job ke 'matched'
	err = s.repo.UpdateJobStatus(ctx, jobID, "matched")
	if err != nil {
		return nil, fmt.Errorf("failed to update job status: %w", err)
	}

	// Filter & sort candidates with match IDs
	scoreMap := make(map[string]float64)
	matchIDMap := make(map[string]string)
	for _, m := range savedMatches {
		scoreMap[m.WorkerID] = m.CompositeScore
		matchIDMap[m.WorkerID] = m.ID
	}
	var matchedCandidates []Candidate
	for i := range candidates {
		if score, ok := scoreMap[candidates[i].WorkerID]; ok {
			candidates[i].CompositeScore = score
			candidates[i].MatchID = matchIDMap[candidates[i].WorkerID]
			matchedCandidates = append(matchedCandidates, candidates[i])
		}
	}

	sort.Slice(matchedCandidates, func(i, j int) bool {
		return matchedCandidates[i].CompositeScore > matchedCandidates[j].CompositeScore
	})

	// Publish Event
	event.GlobalBus.Publish(event.EventJobMatched, map[string]interface{}{
		"job_id":     jobID,
		"candidates": matchedCandidates,
	})

	return matchedCandidates, nil
}

func (s *Service) calculateScores(candidates []Candidate, jobID string, maxDistance float64) []JobMatch {
	var matches []JobMatch

	for _, c := range candidates {
		// 1. Location Score: 1 - (jarak / maxDistance)
		locScore := 1.0 - (c.DistanceMeters / maxDistance)
		if locScore < 0 {
			locScore = 0.0
		}

		// 2. Kerjantara Score Normalized: score / 5.0
		ksNorm := c.KerjantaraScore / 5.0

		// 3. Experience Normalized: min(jobs, 50) / 50.0
		expNorm := math.Min(float64(c.TotalJobsDone), 50.0) / 50.0

		// 4. Response Time Normalized: 1 - min(avg_resp_min, 60) / 60.0
		respNorm := 1.0 - (math.Min(c.AvgResponseMin, 60.0) / 60.0)

		// Composite Score: (location * 0.40) + (ks_norm * 0.30) + (exp_norm * 0.20) + (resp_norm * 0.10)
		composite := (locScore * 0.40) + (ksNorm * 0.30) + (expNorm * 0.20) + (respNorm * 0.10)

		// Detail breakdown disimpan dalam format JSON untuk analisis
		breakdown := map[string]interface{}{
			"location_score":     locScore,
			"ks_normalized":     ksNorm,
			"exp_normalized":    expNorm,
			"response_normalized": respNorm,
			"distance_meters":    c.DistanceMeters,
			"kerjantara_score":   c.KerjantaraScore,
			"total_jobs_done":    c.TotalJobsDone,
			"avg_response_min":   c.AvgResponseMin,
		}

		breakdownJSON, _ := json.Marshal(breakdown)

		matches = append(matches, JobMatch{
			JobID:          jobID,
			WorkerID:       c.WorkerID,
			CompositeScore: composite,
			Breakdown:      string(breakdownJSON),
		})
	}

	return matches
}
