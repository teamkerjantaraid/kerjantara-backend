package job

import "time"

// CreateJobRequest is the request body for creating a new job
type CreateJobRequest struct {
	SkillCatID  int     `json:"skill_cat_id" example:"3"`
	Description string  `json:"description" example:"Butuh tukang las untuk pagar rumah"`
	Budget      int64   `json:"budget" example:"500000"`
	Lat         float64 `json:"lat" example:"-6.2088"`
	Lng         float64 `json:"lng" example:"106.8456"`
	CityCode    string  `json:"city_code" example:"JKT"`
}

// RateCardResponse contains market rate information for a skill category in a city
type RateCardResponse struct {
	MinRate  int64  `json:"min_rate" example:"300000"`
	MaxRate  int64  `json:"max_rate" example:"700000"`
	RateUnit string `json:"rate_unit" example:"per_day"`
	Label    string `json:"label" example:"Harga wajar Tukang Las di JKT: Rp 300000 – 700000/hari"`
}

// CandidateItem represents a worker candidate returned in CreateJobResponse
type CandidateItem struct {
	MatchID          string    `json:"match_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	MatchRank        int       `json:"match_rank" example:"1"`
	WorkerID         string    `json:"worker_id" example:"550e8400-e29b-41d4-a716-446655440001"`
	FullName         string    `json:"full_name" example:"Budi Santoso"`
	KerjantaraScore  float64   `json:"kerjantara_score" example:"4.8"`
	TotalJobsDone    int       `json:"total_jobs_done" example:"42"`
	DistanceKM       float64   `json:"distance_km" example:"1.5"`
	AvgResponseMin   float64   `json:"avg_response_min" example:"5.2"`
	Bio              string    `json:"bio" example:"Tukang las berpengalaman 10 tahun"`
	CompositeScore   float64   `json:"composite_score" example:"0.96"`
	ResponseDeadline time.Time `json:"response_deadline" example:"2024-01-15T08:45:00+07:00"`
}

// CreateJobResponse is the response body after successfully creating a job
type CreateJobResponse struct {
	JobID                 string             `json:"job_id" example:"550e8400-e29b-41d4-a716-446655440002"`
	Status                string             `json:"status" example:"matched"`
	Budget                int64              `json:"budget" example:"500000"`
	RateCard              *RateCardResponse  `json:"rate_card,omitempty"`
	BudgetVsMarket        string             `json:"budget_vs_market" example:"within_range"`
	ResponseWindowMinutes int                `json:"response_window_minutes" example:"15"`
	Candidates            []CandidateItem    `json:"candidates"`
}

// AcceptMatchRequest is the request body for accepting a job match
type AcceptMatchRequest struct {
	MatchID string `json:"match_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// RejectMatchRequest is the request body for rejecting a job match
type RejectMatchRequest struct {
	MatchID string `json:"match_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ArriveRequest is the request body for marking arrival at a job location
type ArriveRequest struct {
	Lat float64 `json:"lat" example:"-6.2088"`
	Lng float64 `json:"lng" example:"106.8456"`
}

// CompleteJobResponse is the response body after a worker marks a job as complete
type CompleteJobResponse struct {
	JobID          string   `json:"job_id" example:"550e8400-e29b-41d4-a716-446655440002"`
	Status         string   `json:"status" example:"done"`
	ProofPhotoURLs []string `json:"proof_photo_urls"`
	ProofFileKeys  []string `json:"proof_file_keys"`
	Message        string   `json:"message" example:"Menunggu konfirmasi pemberi kerja"`
}

// RateJobRequest is the request body for rating a completed job
type RateJobRequest struct {
	Score   float64 `json:"score" example:"4.5"`
	Comment string  `json:"comment" example:"Pekerja sangat profesional dan tepat waktu"`
}

// MatchFallbackRequest is the request body for triggering city-level match fallback
type MatchFallbackRequest struct {
	CityID int `json:"city_id" example:"1"`
}
