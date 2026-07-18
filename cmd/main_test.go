package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSwaggerRouteNotRegisteredInProduction memverifikasi bahwa route /swagger/*
// tidak terdaftar saat APP_ENV=production, sehingga GET /swagger/index.html
// mengembalikan HTTP 404 (Requirements 9.1, 9.2, 9.3).
func TestSwaggerRouteNotRegisteredInProduction(t *testing.T) {
	r := newBaseRouter()
	registerSwaggerRoutes(r, "production")

	req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected HTTP 404 saat APP_ENV=production, got %d", w.Code)
	}
}

// TestSwaggerRouteRegisteredInDevelopment memverifikasi bahwa route /swagger/*
// terdaftar saat APP_ENV=development, sehingga GET /swagger/index.html
// tidak mengembalikan HTTP 404 (Requirements 9.1).
func TestSwaggerRouteRegisteredInDevelopment(t *testing.T) {
	r := newBaseRouter()
	registerSwaggerRoutes(r, "development")

	req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Errorf("expected route /swagger/* terdaftar saat APP_ENV=development, got HTTP 404")
	}
}

// TestSwaggerRouteRegisteredWhenEnvEmpty memverifikasi bahwa route /swagger/*
// terdaftar saat APP_ENV kosong (tidak di-set), sehingga GET /swagger/index.html
// tidak mengembalikan HTTP 404 (Requirements 9.1).
func TestSwaggerRouteRegisteredWhenEnvEmpty(t *testing.T) {
	r := newBaseRouter()
	registerSwaggerRoutes(r, "")

	req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Errorf("expected route /swagger/* terdaftar saat APP_ENV kosong, got HTTP 404")
	}
}

// TestSwaggerRouteNotRegisteredInProductionDocJSON memverifikasi bahwa
// /swagger/doc.json juga mengembalikan 404 saat production (Requirements 9.2, 9.3).
func TestSwaggerRouteNotRegisteredInProductionDocJSON(t *testing.T) {
	r := newBaseRouter()
	registerSwaggerRoutes(r, "production")

	req := httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected HTTP 404 untuk /swagger/doc.json saat APP_ENV=production, got %d", w.Code)
	}
}
