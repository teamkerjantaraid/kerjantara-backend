package auth

// RegisterRequest is the request body for user registration
type RegisterRequest struct {
	FullName string `json:"full_name" example:"Budi Santoso"`
	Phone    string `json:"phone" example:"081234567890"`
	Password string `json:"password" example:"P@ssw0rd123"`
	Role     string `json:"role" example:"worker" enums:"worker,employer"`
}

// RegisterResponse is the response body for successful registration
type RegisterResponse struct {
	UserID   string `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	FullName string `json:"full_name" example:"Budi Santoso"`
	Role     string `json:"role" example:"worker"`
	Token    string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
}

// LoginRequest is the request body for user login
type LoginRequest struct {
	Phone    string `json:"phone" example:"081234567890"`
	Password string `json:"password" example:"P@ssw0rd123"`
}

// LoginResponse is the response body for successful login
type LoginResponse struct {
	UserID         string `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	FullName       string `json:"full_name" example:"Budi Santoso"`
	Role           string `json:"role" example:"worker"`
	VerifStatus    string `json:"verif_status" example:"unverified"`
	Token          string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	TokenExpiresAt string `json:"token_expires_at" example:"2024-01-15T09:30:00+07:00"`
}

// KTPUploadResponse is the response body for KTP upload
type KTPUploadResponse struct {
	VerifStatus string `json:"verif_status" example:"pending"`
	Message     string `json:"message" example:"KTP berhasil diupload, menunggu review"`
}

// ToggleWorkerRequest is the request body for toggling worker availability
type ToggleWorkerRequest struct {
	IsActive bool    `json:"is_active" example:"true"`
	Lat      float64 `json:"lat" example:"-6.2088"`
	Lng      float64 `json:"lng" example:"106.8456"`
}

// ActivateRoleRequest is the request body for activating a role
type ActivateRoleRequest struct {
	Role string `json:"role" example:"worker"`
}

// SwitchRoleRequest is the request body for switching the active role
type SwitchRoleRequest struct {
	Role string `json:"role" example:"employer"`
}

// ReviewVerificationRequest is the request body for reviewing a KTP verification
type ReviewVerificationRequest struct {
	Decision string `json:"decision" example:"approved" enums:"approved,rejected,resubmit"`
	Note     string `json:"note" example:"KTP terlihat jelas dan valid"`
}
