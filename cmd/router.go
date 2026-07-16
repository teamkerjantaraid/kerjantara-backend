package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	httpSwagger "github.com/swaggo/http-swagger"
)

// registerSwaggerRoutes mendaftarkan route /swagger/* ke router hanya jika
// appEnv bukan "production". Ini memastikan Swagger UI tidak terekspos di
// lingkungan produksi (Requirements 9.1, 9.2, 9.3).
func registerSwaggerRoutes(r chi.Router, appEnv string) {
	if appEnv != "production" {
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
