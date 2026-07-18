# Kerjantara Backend — Frontend Integration Guide

## Setup Cepat

### 1. Generate TypeScript types dari Swagger
```bash
# Di folder frontend
npx openapi-typescript https://api.kerjantara.id/swagger/doc.json -o src/lib/api/schema.d.ts
# Atau dari local dev:
npx openapi-typescript http://localhost:8080/swagger/doc.json -o src/lib/api/schema.d.ts
```

### 2. API Client wrapper (`src/lib/api/client.ts`)
```typescript
const BASE = import.meta.env.VITE_API_URL || 'http://localhost:8080';

let token: string | null = null;

export function setToken(t: string) { token = t; localStorage.setItem('jwt', t); }
export function getToken() { return token || localStorage.getItem('jwt'); }

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  const jwt = getToken();
  if (jwt) headers['Authorization'] = `Bearer ${jwt}`;

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });

  const json = await res.json();
  if (!res.ok) throw new ApiError(json.error);
  return json.data;
}

export const api = {
  get:    <T>(path: string)                    => request<T>('GET', path),
  post:   <T>(path: string, body?: unknown)    => request<T>('POST', path, body),
  patch:  <T>(path: string, body?: unknown)    => request<T>('PATCH', path, body),
  upload: <T>(path: string, formData: FormData) => {
    const jwt = getToken();
    return fetch(`${BASE}${path}`, {
      method: 'POST',
      headers: jwt ? { Authorization: `Bearer ${jwt}` } : {},
      body: formData,
    }).then(r => r.json()).then(j => j.data);
  },
};

class ApiError extends Error {
  code: string;
  constructor(e: { code: string; message: string }) { super(e.message); this.code = e.code; }
}
```

### 3. WebSocket connection (`src/lib/ws/socket.ts`)
```typescript
export function connectWS(): WebSocket {
  const jwt = getToken();
  const ws = new WebSocket(`ws://localhost:8080/ws?token=${jwt}`);
  // atau wss:// untuk production
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    // dispatch ke Svelte store: { type, payload, timestamp }
  };
  ws.onclose = () => setTimeout(connectWS, 3000); // auto-reconnect
  return ws;
}
```

---

## FLOW 1: Registrasi & Login

```
┌──────────┐     POST /auth/register     ┌──────────┐
│ Landing   │ ──────────────────────────▶ │ Backend   │
│ Page      │ ◀──── token + user_id ──── │           │
└──────────┘                             └──────────┘
     │
     ├── "Saya Pekerja" → register { role: "worker" }
     └── "Saya Pemberi Kerja" → register { role: "employer" }
```

| Step | Method | Endpoint | Body |
|------|--------|----------|------|
| Register | `POST` | `/auth/register` | `{ full_name, phone, password, role }` |
| Login | `POST` | `/auth/login` | `{ phone, password }` |
| Get profile | `GET` | `/auth/me` | — |

### Mock data
```typescript
export const mockRegisterRes = {
  user_id: 'uuid-xxx',
  full_name: 'Pak Budi',
  role: 'worker',
  token: 'eyJ...',
};
```

---

## FLOW 2: Verifikasi KTP

```
┌──────────┐   POST /auth/ktp/upload    ┌──────────┐
│ User      │ ── (multipart: ktp_photo,  │ Backend   │
│ Upload KTP│    selfie_photo) ────────▶ │           │
└──────────┘ ◀── verif_status: pending ──│           │
                                         │           │
┌──────────┐   GET /admin/ktp/pending    │           │
│ Admin     │ ◀── list pending ──────────│           │
│ Panel     │   PATCH /admin/ktp/:id/review { decision: "approved" }
└──────────┘                              └──────────┘
```

| Step | Method | Endpoint | Auth | Body |
|------|--------|----------|------|------|
| Upload KTP | `POST` | `/auth/ktp/upload` | JWT | `FormData(ktp_photo, selfie_photo)` |
| Cek status | `GET` | `/auth/me` | JWT | — (cek `verif_status`) |
| Admin list | `GET` | `/admin/ktp/pending` | JWT + role admin | — |
| Admin review | `PATCH` | `/admin/ktp/:user_id/review` | JWT + role admin | `{ decision, note }` |

### Mock data
```typescript
export const mockKTPUploadRes = {
  verif_status: 'pending',
  message: 'KTP berhasil diupload, menunggu review',
};
```

---

## FLOW 3: Pekerja Aktifkan Status & Role Switch

```
┌──────────┐ PATCH /auth/worker/toggle   ┌──────────┐
│ Worker    │ { is_active: true, lat, lng }──▶     │
│ Dashboard │ ◀── OK ──────────────────────│ Backend  │
└──────────┘                              │           │
                                          │           │
┌──────────┐ POST /auth/roles/activate    │           │
│ Employer  │ { role: "worker" } ───────▶ │           │
│ → Worker  │ ◀── token baru ─────────────│           │
└──────────┘                              └──────────┘
```

| Step | Method | Endpoint | Body |
|------|--------|----------|------|
| Toggle aktif | `PATCH` | `/auth/worker/toggle` | `{ is_active, lat, lng }` |
| Activate role | `POST` | `/auth/roles/activate` | `{ role }` |
| Switch role | `PATCH` | `/auth/roles/switch` | `{ role }` |

---

## FLOW 4: Buat Job & Matching Engine (Pemberi Kerja)

Ini flow paling penting — **matching engine jalan synchronously di `POST /jobs`**, response langsung berisi 3 kandidat terbaik.

```
┌──────────┐
│ Employer  │
│ Buat Job  │── POST /jobs ──────────────────────────────────────┐
│           │   { skill_cat_id, description, budget, lat, lng }   │
└──────────┘                                                     ▼
     │                                                  ┌─────────────────┐
     │                                                  │  MATCHING ENGINE │
     │                                                  │ 1. Hard filter   │
     │                                                  │ 2. Weight score  │
     │                                                  │ 3. Top 3 return  │
     │                                                  └────────┬────────┘
     │                                                           │
     ◀──────── Response langsung berisi 3 candidates ────────────┘
         { status: "matched", candidates: [...], rate_card: {...} }
```

### REQUEST
```json
POST /jobs
{
  "skill_cat_id": 2,
  "description": "Butuh ART bersih-bersih rumah 2 lantai",
  "budget": 200000,
  "lat": -6.2088,
  "lng": 106.8456,
  "city_code": "JAKARTA"
}
```

### RESPONSE (3 kandidat)
```json
{
  "job_id": "uuid-job",
  "status": "matched",
  "budget": 200000,
  "rate_card": {
    "min_rate": 100000,
    "max_rate": 300000,
    "rate_unit": "per_day",
    "label": "Harga wajar ART Harian di Jakarta: Rp 100.000 – 300.000/hari"
  },
  "budget_vs_market": "within_range",
  "response_window_minutes": 15,
  "candidates": [
    {
      "match_id": "uuid-match-1",       // PAKAI INI untuk accept/reject
      "match_rank": 1,
      "worker_id": "uuid-worker-1",
      "full_name": "Siti Aminah",
      "kerjantara_score": 4.5,
      "total_jobs_done": 20,
      "distance_km": 0.8,
      "avg_response_min": 5,
      "bio": "ART berpengalaman, jujur dan teliti",
      "composite_score": 0.87,
      "response_deadline": "2026-07-01T08:15:00Z"
    }
    // ... max 3 kandidat
  ]
}
```

### Edge case: tidak ada kandidat
```json
{
  "job_id": "uuid-job",
  "status": "pending_city_fallback",
  "candidates": []
}
```
→ FE tampilkan UI "Cari di kota lain?" → panggil `POST /jobs/:id/match-fallback { city_id }`

### Mock
```typescript
export const mockCandidates = [
  {
    match_id: 'uuid-match-1', match_rank: 1,
    worker_id: 'uuid-worker-1', full_name: 'Siti Aminah',
    kerjantara_score: 4.5, total_jobs_done: 20,
    distance_km: 0.8, avg_response_min: 5,
    bio: 'ART berpengalaman, jujur dan teliti',
    composite_score: 0.87,
    response_deadline: new Date(Date.now() + 15*60*1000).toISOString(),
  },
  // ... 2 lagi
];
```

---

## FLOW 5: Accept / Reject Job (Pekerja)

Pekerja menerima notifikasi via WebSocket (`job.matched`) atau polling `GET /jobs/worker`.

```
┌──────────┐ PATCH /jobs/:id/accept-match
│ Worker    │ { match_id: "uuid-match-1" } ──▶ ┌──────────┐
│ Accept    │ ◀── agreed_price, fee ────────────│ Backend   │
└──────────┘                                    │           │
                                                │ FOR UPDATE│
┌──────────┐ PATCH /jobs/:id/reject-match       │ row lock  │
│ Worker    │ { match_id: "uuid-match-1" } ──▶ │           │
│ Reject    │ ◀── next_candidate_notified ──────│           │
└──────────┘                                    └──────────┘
```

### REQUEST — Accept
```json
PATCH /jobs/:id/accept-match
{ "match_id": "uuid-match-1" }
```

### RESPONSE — Accept
```json
{
  "job_id": "uuid-job",
  "match_id": "uuid-match-1",
  "agreed_price": 200000,
  "platform_fee": 10000,
  "net_to_worker": 200000,
  "total_charged_to_employer": 210000,
  "status": "accepted",
  "message": "Job berhasil diterima. Menunggu pemberi kerja mengamankan dana."
}
```

### Error 409 — Job sudah diambil orang lain
```json
{ "error": { "code": "JOB_TAKEN", "message": "Job sudah diterima oleh pekerja lain" } }
```

### REQUEST — Reject
```json
PATCH /jobs/:id/reject-match
{ "match_id": "uuid-match-1" }
```

### RESPONSE — Reject
```json
{
  "job_id": "uuid-job",
  "match_id": "uuid-match-1",
  "message": "Job berhasil dilewati",
  "next_candidate_notified": true
}
```

---

## FLOW 6: Pembayaran (Employer) — Midtrans Snap

Setelah worker accept → employer bayar via Midtrans Snap.

```
┌──────────┐ POST /payments/create { job_id }
│ Employer  │ ─────────────────────────────────▶ ┌──────────┐
│ Bayar     │ ◀── snap_token ────────────────────│ Backend   │
└──────────┘                                     │           │
     │                                           └──────────┘
     ▼ Midtrans Snap UI (pakai snap_token)
┌──────────┐
│ Midtrans  │── webhook ──▶ POST /payments/webhook
└──────────┘                → status payment = "held"
```

### REQUEST
```json
POST /payments/create
{ "job_id": "uuid-job" }
```

### RESPONSE
```json
{
  "payment_id": "uuid-payment",
  "snap_token": "snap-xxxx",
  "agreed_price": 200000,
  "platform_fee": 10000,
  "net_to_worker": 200000,
  "total_charged_to_employer": 210000,
  "midtrans_order_id": "KJT-20260701-xxxx",
  "fee_note": "Biaya layanan Rp 10.000 (transaksi di bawah Rp 1.000.000)"
}
```

> FE pakai `snap_token` untuk Midtrans Snap: `window.snap.pay(snap_token, { onSuccess, onPending, onError })`

### Cek status
```json
GET /payments/:job_id
→ { payment_id, status: "held"|"released"|"pending", ... }
```

### Mock
```typescript
export const mockPaymentCreate = {
  payment_id: 'uuid-payment',
  snap_token: 'mock-snap-token',
  agreed_price: 200000,
  platform_fee: 10000,
  net_to_worker: 200000,
  total_charged_to_employer: 210000,
  midtrans_order_id: 'KJT-20260701-mock',
  fee_note: 'Biaya layanan Rp 10.000 (transaksi di bawah Rp 1.000.000)',
};
```

---

## FLOW 7: Pekerja Sampai → Kerja → Selesai

```
┌──────────┐ PATCH /jobs/:id/arrive
│ Worker    │ { lat, lng } ──────────────────▶ ┌──────────┐
│ "Sampai"  │ ◀── { gps_verified, distance }───│ Backend   │
└──────────┘                                    │           │
                                                │           │
┌──────────┐ POST /jobs/:id/complete            │           │
│ Worker    │ FormData(proof_photos[], notes) ─▶ │           │
│ "Selesai" │ ◀── { proof_photo_urls } ──────────│           │
└──────────┘                                    │           │
                                                │           │
┌──────────┐ PATCH /jobs/:id/confirm            │           │
│ Employer  │ ────────────────────────────────▶ │           │
│ Konfirmasi│ ◀── { payment_status: released }──│           │
└──────────┘                                    │           │
                                                │           │
┌──────────┐ POST /jobs/:id/rate                │           │
│ Rating    │ { score, comment } ─────────────▶ │           │
│ (2 arah)  │ ◀── OK ───────────────────────────│           │
└──────────┘                                    └──────────┘
```

### Arrive
```json
PATCH /jobs/:id/arrive
{ "lat": -6.2100, "lng": 106.8460 }

→ { job_id, arrived_at, gps_verified: true, distance_from_job_m: 32 }
→ Error GPS_TOO_FAR: { error: { code: "GPS_TOO_FAR", message: "..." } }
```

### Complete (multipart)
```
POST /jobs/:id/complete
Content-Type: multipart/form-data
  proof_photos[]: <File>  (bisa multiple)
  notes: "Lantai sudah dipel"

→ { job_id, status: "done", proof_photo_urls: [...], proof_file_keys: [...], message: "Menunggu konfirmasi..." }
```

### Confirm
```json
PATCH /jobs/:id/confirm
(no body)

→ { job_id, status: "done", payment_status: "released", message: "Dana berhasil dilepaskan ke pekerja" }
```

### Rate
```json
POST /jobs/:id/rate
{ "score": 4.5, "comment": "Kerja bagus, bersih dan tepat waktu" }

→ { rating_id, ratee_name, score, message: "Rating berhasil disimpan" }
```

---

## FLOW 8: WebSocket Events

Koneksi: `ws://localhost:8080/ws?token=<JWT>`

| Event | Diterima oleh | Payload |
|-------|--------------|---------|
| `job.matched` | Worker | `{ job_id, message }` |
| `job.accepted` | Employer | `{ job_id, worker_name, message }` |
| `job.completed` | Employer | `{ job_id, message }` (pekerja upload bukti) |
| `job.completed` | Worker | `{ job_id, message }` (employer konfirmasi) |
| `payment.released` | Worker | `{ job_id, message }` |
| `ktp.review_needed` | Admin | (internal) |

Format message:
```json
{
  "type": "job.matched",
  "payload": { "job_id": "...", "message": "Ada pekerjaan baru yang cocok!" },
  "timestamp": "2026-07-01T08:00:05Z"
}
```

---

## Referensi — Semua Endpoint Sekilas

| Method | Endpoint | Auth | Keterangan |
|--------|----------|------|------------|
| `POST` | `/auth/register` | — | Registrasi |
| `POST` | `/auth/login` | — | Login |
| `POST` | `/auth/ktp/upload` | JWT | Upload KTP (multipart) |
| `GET` | `/auth/me` | JWT | Profil user |
| `PATCH` | `/auth/worker/toggle` | JWT | Toggle aktif + GPS |
| `POST` | `/auth/roles/activate` | JWT | Aktifkan role baru |
| `PATCH` | `/auth/roles/switch` | JWT | Ganti role aktif |
| `POST` | `/jobs` | JWT | Buat job + trigger matching |
| `GET` | `/jobs/:id` | JWT | Detail job |
| `GET` | `/jobs/employer` | JWT | List job employer |
| `GET` | `/jobs/worker` | JWT | List job worker |
| `PATCH` | `/jobs/:id/accept-match` | JWT | Worker accept job |
| `PATCH` | `/jobs/:id/reject-match` | JWT | Worker reject job |
| `PATCH` | `/jobs/:id/arrive` | JWT | Worker tap "sampai" |
| `POST` | `/jobs/:id/complete` | JWT | Upload bukti (multipart) |
| `PATCH` | `/jobs/:id/confirm` | JWT | Employer konfirmasi |
| `POST` | `/jobs/:id/rate` | JWT | Rating |
| `POST` | `/jobs/:id/match-fallback` | JWT | Fallback cari kota lain |
| `POST` | `/payments/create` | JWT | Buat pembayaran |
| `GET` | `/payments/:job_id` | JWT | Status pembayaran |
| `GET` | `/payments/:job_id/milestones` | JWT | Milestone (multi-hari) |
| `POST` | `/payments/webhook` | — | Midtrans webhook |
| `GET` | `/ref/rate-cards` | — | Panduan harga |
| `GET` | `/ref/skill-categories` | — | Kategori skill |
| `GET` | `/workers/:id/score` | JWT | KerjantaraScore |
| `GET` | `/ws` | JWT (query) | WebSocket |
| `GET` | `/admin/ktp/pending` | JWT+admin | List KTP pending |
| `PATCH` | `/admin/ktp/:user_id/review` | JWT+admin | Review KTP |

---

## Error Response Convention

Semua response error mengikuti format:
```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Deskripsi bahasa Indonesia"
  }
}
```

| Code | HTTP | Arti |
|------|------|------|
| `UNAUTHORIZED` | 401 | Token tidak valid/expired |
| `FORBIDDEN` | 403 | Tidak punya akses |
| `NOT_FOUND` | 404 | Resource tidak ditemukan |
| `VALIDATION_ERROR` | 422 | Input tidak valid |
| `JOB_TAKEN` | 409 | Job sudah diambil orang lain |
| `GPS_TOO_FAR` | 422 | Koordinat > 50m dari lokasi |
| `INTERNAL_ERROR` | 500 | Error server |

---

## Environment Variables (Frontend)

```env
# .env
VITE_API_URL=http://localhost:8080
# Production: https://kerjantara-backend-production.up.railway.app
```
