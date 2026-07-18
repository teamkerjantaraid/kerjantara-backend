package docs

// ErrorDetail contains error code and message
type ErrorDetail struct {
	Code    string `json:"code" example:"VALIDATION_ERROR"`
	Message string `json:"message" example:"Field tidak valid"`
}

// ErrorEnvelope is the standard error response wrapper
type ErrorEnvelope struct {
	Error ErrorDetail `json:"error"`
}

// MetaInfo contains response metadata
type MetaInfo struct {
	Timestamp string `json:"timestamp" example:"2024-01-15T08:30:00+07:00"`
}

// SuccessEnvelope is the standard success response wrapper
type SuccessEnvelope struct {
	Data interface{} `json:"data"`
	Meta *MetaInfo   `json:"meta,omitempty"`
}
