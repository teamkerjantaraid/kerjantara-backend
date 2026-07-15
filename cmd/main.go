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

	"kerjantara-backend/internal/auth"
	"kerjantara-backend/internal/job"
	"kerjantara-backend/internal/matching"
	"kerjantara-backend/internal/notification"
	"kerjantara-backend/internal/payment"
	"kerjantara-backend/internal/score"
	"kerjantara-backend/pkg/config"
	"kerjantara-backend/pkg/database"
	customMiddleware "kerjantara-backend/pkg/middleware"
	"kerjantara-backend/pkg/storage"
	"kerjantara-backend/pkg/event"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
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
	dbPool, err := database.InitDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v\n", err)
	}
	defer database.CloseDB()

	// 3. Initialize File Storage client (S3 / MinIO / Supabase Storage)
	storageEndpoint := os.Getenv("STORAGE_ENDPOINT")
	if storageEndpoint == "" {
		storageEndpoint = cfg.SupabaseURL
	}
	storageAccessKey := os.Getenv("STORAGE_ACCESS_KEY")
	if storageAccessKey == "" {
		storageAccessKey = cfg.SupabaseServiceKey
	}
	storageSecretKey := os.Getenv("STORAGE_SECRET_KEY")
	if storageSecretKey == "" {
		storageSecretKey = cfg.SupabaseServiceKey
	}

	if storageEndpoint != "" {
		_, err = storage.InitStorage(storageEndpoint, storageAccessKey, storageSecretKey, cfg.SupabaseStorageBucket)
		if err != nil {
			log.Printf("Warning: failed to initialize storage client (files upload might fail): %v\n", err)
		} else {
			log.Println("Storage client initialized successfully")
		}
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
	authService := auth.NewService(authRepo, cfg.JWTSecret)
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
		_, _ = w.Write([]byte(`{"status":"running","service":"kerjantara-backend-monolith","version":"v3.3-hackathon"}`))
	})

	// Auth Middleware
	jwtAuthMiddleware := customMiddleware.JWTAuth(cfg.JWTSecret)

	// Register Routes
	authHandler.RegisterRoutes(r, jwtAuthMiddleware)
	jobHandler.RegisterRoutes(r, jwtAuthMiddleware)
	scoreHandler.RegisterRoutes(r)
	paymentHandler.RegisterRoutes(r, jwtAuthMiddleware)
	notificationHandler.RegisterRoutes(r, jwtAuthMiddleware)

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
