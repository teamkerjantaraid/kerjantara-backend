# Kerjantara.id — Backend Monolith Modular
> **Platform Job Matching Pekerja Informal Terverifikasi**  
> *DIGDAYA Team — Hackathon submission v3.3 (Juli 2026)*

---

## 📌 Gambaran Umum
Kerjantara.id dirancang menggunakan arsitektur **Go Monolith Modular**. Seluruh modul bisnis (Auth, Job, Matching, Score, Payment, dan Notification) dikemas di dalam satu binary untuk meminimalkan kompleksitas orkestrasi, namun dipisahkan secara struktural untuk memudahkan transisi menuju arsitektur microservices pada fase berikutnya.

Aplikasi ini menggunakan **PostgreSQL + PostGIS** untuk pencarian berbasis lokasi (*geo-spatial*), **Supabase Storage (S3-compatible)** untuk berkas dokumen KTP/Selfie terenkripsi, **WebSocket** untuk pengiriman notifikasi instan, serta escrow gateway simulasi menggunakan **Midtrans Sandbox**.

---

## 🛠️ Tech Stack
*   **Language:** Go 1.25
*   **HTTP Router:** Chi Router v5
*   **Database:** PostgreSQL 15 + PostGIS 3.3 (Supabase / Local Docker)
*   **Storage (S3-compatible):** Supabase Storage / MinIO (lokal)
*   **Real-time Push:** Gorilla WebSocket (Goroutine-based Hub)
*   **Payment Gateway:** Midtrans Sandbox API
*   **Containerization:** Docker + Docker Compose

---

## 📂 Struktur Repository
```
kerjantara-backend/
├── cmd/
│   └── main.go                    # Entry point aplikasi
├── internal/                      # Modul Domain Bisnis Terisolasi
│   ├── auth/                      # Registrasi, Login, KTP KYC, JWT
│   ├── job/                       # Daur hidup pekerjaan, rating, bukti kerja
│   ├── matching/                  # Algoritma PostGIS spasial & fallback kota
│   ├── notification/              # Real-time WebSocket hub
│   ├── payment/                   # Midtrans Escrow & Milestone Payment harian
│   └── score/                     # Reputasi KerjantaraScore & log audit
├── pkg/                           # Pustaka Pendukung / Shared Helpers
│   ├── config/                    # Loader environment variables
│   ├── database/                  # Pool koneksi pgxpool
│   ├── event/                     # Event Bus internal (Go Channel Pub/Sub)
│   ├── middleware/                # JWT authenticator & CORS handler
│   └── storage/                   # Client upload S3/Supabase Storage
├── migrations/
│   └── 001_init_schema.sql        # Skema DDL & Seed Data lengkap
├── Dockerfile                     # Multi-stage build production image
├── docker-compose.yml             # PostgreSQL + PostGIS + MinIO + Backend (lokal)
└── .env.example                   # Contoh konfigurasi environment
```

---

## 🚀 Fitur Unggulan & Mekanisme Bisnis
1.  **Satu Identitas, Multi-Role (Dual-Role):**  
    Satu akun pengguna (KTP terverifikasi) dapat mengaktifkan peran pekerja (*worker*) dan pemberi kerja (*employer*) secara bersamaan. Pergantian peran dilakukan secara dinamis via *role-switcher* tanpa perlu mengunggah ulang dokumen KYC.
2.  **Auto-Expand Radius Matching Engine:**  
    Pencarian pekerja dimulai dari radius 2km, secara otomatis melebar (+2km) jika kandidat aktif kurang dari 3, hingga batas maksimal 10km.
3.  **Fallback Kota Terdekat:**  
    Jika radius 10km habis tanpa kandidat memadai, sistem menawarkan employer opsi pencarian berdasarkan batas wilayah kota terdekat (*city centroid sorting*).
4.  **Mekanisme Pencegahan Double-Accept (Race Condition):**  
    Penggunaan database transaction dengan **Row-Locking (`SELECT ... FOR UPDATE`)** untuk menjamin tidak ada dua pekerja yang dapat menyetujui satu pekerjaan yang sama secara bersamaan.
5.  **Milestone Payment Harian (Multi-Day Job):**  
    Mendukung penahanan dana escrow total di awal transaksi menggunakan Midtrans Snap, namun dialokasikan secara proporsional ke saldo KitaDompet pekerja per hari kerja setelah dikonfirmasi oleh employer.
6.  **Real-Time Push Notification:**  
    WebSocket Hub yang efisien untuk mem-push notifikasi instan langsung dari internal event bus (seperti penawaran kerja baru, konfirmasi kedatangan, pembayaran cair).

---

## ⚙️ Panduan Instalasi & Menjalankan

### 1. Inisialisasi Database (Supabase / PostgreSQL Remote)
Sebelum menjalankan backend, pastikan database PostgreSQL Anda telah siap:
1.  Aktifkan ekstensi spasial **PostGIS** dan generator **UUID** di SQL Editor Supabase Anda:
    ```sql
    CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
    CREATE EXTENSION IF NOT EXISTS postgis;
    ```
2.  Jalankan file skema migrasi [migrations/001_init_schema.sql](migrations/001_init_schema.sql) di dalam SQL Editor Supabase untuk membangun seluruh tabel relasional, fungsi pemicu, indeks PostGIS, dan data referensi awal.
3.  Buat **Private Storage Bucket** di Supabase Storage bernama `kerjantara` dan buat folder `ktp/`, `selfie/`, dan `proof/` di dalamnya.

### 2. Setup Environment Variables
Salin berkas `.env.example` menjadi `.env` di root folder backend:
```bash
cp .env.example .env
```
Buka file `.env` tersebut dan lengkapi nilai kredensial Supabase, database password, dan kunci rahasia JWT Anda.

### 3. Menjalankan Server secara Lokal
Untuk menjalankan server secara manual di port default `8080`:
```bash
go run cmd/main.go
```

Untuk menjalankan seluruh ekosistem (termasuk PostgreSQL lokal dan MinIO S3 lokal) via Docker Compose:
```bash
docker compose up --build
```

---

## 📄 API Contract & Endpoint
Detail request body, response JSON, dan WebSocket event payload dapat diakses secara detail pada dokumen **[API_Contract_Kerjantara.md](../API_Contract_Kerjantara.md)** di folder proyek utama.

---
*Kerjantara.id — DIGDAYA x Hackathon 2026*
