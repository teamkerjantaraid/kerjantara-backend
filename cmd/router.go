package main

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	httpSwagger "github.com/swaggo/http-swagger"
	"kerjantara-backend/docs"
)

// registerSwaggerRoutes mendaftarkan route /swagger/* ke router hanya jika
// appEnv bukan "production". Ini memastikan Swagger UI tidak terekspos di
// lingkungan produksi (Requirements 9.1, 9.2, 9.3).
// Host Swagger dikonfigurasi melalui env APP_HOST (default: localhost:8080).
func registerSwaggerRoutes(r chi.Router, appEnv string) {
	if appEnv != "production" {
		host := os.Getenv("APP_HOST")
		if host == "" {
			host = "localhost:8080"
		}
		docs.SwaggerInfo.Host = host

		r.Get("/swagger/*", httpSwagger.WrapHandler)
	}
}

// newBaseRouter membuat chi.Router baru dengan satu endpoint root GET /.
// Digunakan oleh test agar tidak perlu melakukan inisialisasi penuh (DB, storage, dll).
func newBaseRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}
