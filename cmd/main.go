package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "kerjantara-backend/docs"
	"kerjantara-backend/internal/auth"
	"kerjantara-backend/internal/job"
	"kerjantara-backend/internal/matching"
	"kerjantara-backend/internal/notification"
	"kerjantara-backend/internal/payment"
	"kerjantara-backend/internal/score"
	"kerjantara-backend/pkg/config"
	"kerjantara-backend/pkg/database"
	"kerjantara-backend/pkg/event"
	customMiddleware "kerjantara-backend/pkg/middleware"
	"kerjantara-backend/pkg/storage"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// @title           Kerjantara Backend API
// @version         v3.4.0
// @description     Backend monolith modular untuk platform jasa informal Kerjantara.id. Menghubungkan employer dengan worker melalui matching engine berbasis GPS, skor reputasi, multi-day job support, dan pembayaran escrow Midtrans.
// @host            localhost:8080
// @BasePath        /

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Masukkan token JWT dalam format: Bearer <token>

func main() {
	// Redirect log output to stdout so Railway does not treat logs as errors.
	log.SetOutput(os.Stdout)

	log.Println("Starting Kerjantara.id Backend Monolith Modular...")

	// 1. Load Configurations
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v\n", err)
	}

	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL / SUPABASE_DB_URL is not set in environment variables")
	}

	// 2. Initialize Database connection pool (Postgres + PostGIS)
	dbPool, err := database.ConnectDB()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v\n", err)
	}

	// 3. Initialize File Storage client (Supabase Storage REST API / MinIO)
	// Supabase mode: uses SUPABASE_URL + SUPABASE_SERVICE_KEY automatically.
	// MinIO mode:    set STORAGE_ENDPOINT, STORAGE_ACCESS_KEY, STORAGE_SECRET_KEY.
	storageEndpoint := os.Getenv("STORAGE_ENDPOINT")
	storageAccessKey := os.Getenv("STORAGE_ACCESS_KEY")
	storageSecretKey := os.Getenv("STORAGE_SECRET_KEY")

	// Use Supabase URL as endpoint fallback so InitStorage can detect Supabase mode.
	if storageEndpoint == "" && cfg.SupabaseURL != "" {
		storageEndpoint = cfg.SupabaseURL
	}

	if storageEndpoint != "" {
		log.Printf("Storage init: endpoint=%s bucket=%s", storageEndpoint, cfg.SupabaseStorageBucket)
		_, err = storage.InitStorage(storageEndpoint, storageAccessKey, storageSecretKey, cfg.SupabaseStorageBucket)
		if err != nil {
			log.Fatalf("FATAL: failed to initialize storage client: %v\n", err)
		}
		log.Println("Storage client initialized successfully")
	} else {
		log.Println("Warning: STORAGE_ENDPOINT / SUPABASE_URL not configured. File uploads are disabled.")
	}

	// 4. Setup Context and Event Bus
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	globalBus := event.GlobalBus

	// 5. Initialize Modular Repositories and Services
	// Auth Module
	authRepo := auth.NewRepository(dbPool)
	authService := auth.NewService(authRepo, cfg.JWTSecret, cfg.SupabaseURL)
	authHandler := auth.NewHandler(authService)

	// Matching Module
	matchingRepo := matching.NewRepository(dbPool)
	matchingService := matching.NewService(matchingRepo, cfg.ResponseWindowMinutes)

	// Job Module
	jobRepo := job.NewRepository(dbPool)
	jobService := job.NewService(jobRepo, matchingService)
	jobHandler := job.NewHandler(jobService, cfg.GPSToleranceMeters)

	// Score Module
	scoreRepo := score.NewRepository(dbPool)
	scoreService := score.NewService(scoreRepo)
	scoreHandler := score.NewHandler(scoreService)

	// Payment Module
	paymentRepo := payment.NewRepository(dbPool)
	paymentService := payment.NewService(paymentRepo, jobRepo, cfg.MidtransServerKey)
	paymentHandler := payment.NewHandler(paymentService)

	// Notification Module
	notificationHub := notification.GlobalHub
	notificationHandler := notification.NewHandler(notificationHub)

	// 6. Start Module Event Listeners
	scoreService.StartEventListener(ctx, globalBus)
	paymentService.StartEventListener(ctx, globalBus)
	notificationHub.StartEventListener(ctx, globalBus)

	// 7. Setup Router and Middlewares
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(customMiddleware.CORS())

	// Root endpoint
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"running","service":"kerjantara-backend-monolith","version":"v3.4.0"}`))
	})

	// Auth Middleware
	jwtAuthMiddleware := customMiddleware.JWTAuth(cfg.JWTSecret)

	// Register Routes
	authHandler.RegisterRoutes(r, jwtAuthMiddleware)
	jobHandler.RegisterRoutes(r, jwtAuthMiddleware)
	scoreHandler.RegisterRoutes(r)
	paymentHandler.RegisterRoutes(r, jwtAuthMiddleware)
	notificationHandler.RegisterRoutes(r, jwtAuthMiddleware)

	// Swagger UI — only available in non-production environments
	registerSwaggerRoutes(r, os.Getenv("APP_ENV"))

	// 8. Start HTTP Server with Graceful Shutdown
	srv := &http.Server{
		Addr:    ":" + cfg.AppPort,
		Handler: r,
	}

	go func() {
		log.Printf("Server listening on port %s\n", cfg.AppPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server ListenAndServe error: %v\n", err)
		}
	}()

	// Wait for terminate signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server gracefully...")
	cancel() // Cancel context to stop listeners

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v\n", err)
	}

	log.Println("Server exited successfully")
}


