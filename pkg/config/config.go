package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL            string
	SupabaseURL            string
	SupabaseServiceKey     string
	SupabaseJWTSecret      string
	SupabaseStorageBucket  string
	MidtransServerKey      string
	JWTSecret              string
	AppPort                string
	ResponseWindowMinutes int
	GPSToleranceMeters     float64
}

var GlobalConfig *Config

func LoadConfig() (*Config, error) {
	// Load .env if exists
	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("SUPABASE_DB_URL")
	}

	responseWindow, err := strconv.Atoi(getEnv("RESPONSE_WINDOW_MINUTES", "15"))
	if err != nil {
		responseWindow = 15
	}

	gpsTolerance, err := strconv.ParseFloat(getEnv("GPS_TOLERANCE_METERS", "50"), 64)
	if err != nil {
		gpsTolerance = 50.0
	}

	cfg := &Config{
		DatabaseURL:            dbURL,
		SupabaseURL:            os.Getenv("SUPABASE_URL"),
		SupabaseServiceKey:     os.Getenv("SUPABASE_SERVICE_KEY"),
		SupabaseJWTSecret:      os.Getenv("SUPABASE_JWT_SECRET"),
		SupabaseStorageBucket:  getEnv("SUPABASE_STORAGE_BUCKET", "kerjantara"),
		MidtransServerKey:      os.Getenv("MIDTRANS_SERVER_KEY"),
		JWTSecret:              getEnv("JWT_SECRET", "super-secret-key-that-is-at-least-32-chars-long"),
		AppPort:                getEnv("APP_PORT", "8080"),
		ResponseWindowMinutes: responseWindow,
		GPSToleranceMeters:     gpsTolerance,
	}

	GlobalConfig = cfg
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
