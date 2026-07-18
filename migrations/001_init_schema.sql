-- =========================================================================
-- FILE: kerjantara_full_schema.sql
-- DESKRIPSI: DDL Skema Penuh untuk MVP Monolith Modular Kerjantara.id
-- KONTEKS: DIGDAYA x Hackathon 2026 - Bersih & Siap Migrasi
-- VERSI: v1.3 — Dual Role Support (worker + employer dalam satu akun)
-- =========================================================================

-- =========================================================================
-- 1. INISIALASI SKEMA & EKSTENSI
-- =========================================================================
CREATE SCHEMA IF NOT EXISTS kerjantara;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS postgis;

-- =========================================================================
-- 2. REFERENCE TABLES (kerjantara.ref_xxxx) - Lookup Data Statis
-- =========================================================================

CREATE TABLE IF NOT EXISTS kerjantara.ref_verif_statuses (
    id         SMALLSERIAL PRIMARY KEY,
    code       VARCHAR(20)  NOT NULL UNIQUE, -- pending, approved, rejected, resubmit
    label      VARCHAR(80)  NOT NULL,
    sort_order SMALLINT     DEFAULT 0
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_user_roles (
    id       SMALLSERIAL PRIMARY KEY,
    code     VARCHAR(20)  NOT NULL UNIQUE, -- worker, employer, admin, reviewer
    label    VARCHAR(80)  NOT NULL
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_job_statuses (
    id         SMALLSERIAL PRIMARY KEY,
    code       VARCHAR(20)  NOT NULL UNIQUE,
    label      VARCHAR(80)  NOT NULL,
    sort_order SMALLINT     DEFAULT 0
);

-- Hierarki 2 level: Level 1 = Grup (parent_id NULL), Level 2 = Spesifik
-- Hanya Level 2 yang boleh dipakai di trx_jobs.skill_cat_id
CREATE TABLE IF NOT EXISTS kerjantara.ref_skill_categories (
    id         SMALLINT     PRIMARY KEY,
    parent_id  SMALLINT     REFERENCES kerjantara.ref_skill_categories(id) ON DELETE RESTRICT,
    code       VARCHAR(30)  NOT NULL UNIQUE,
    label_id   VARCHAR(80)  NOT NULL,
    icon_key   VARCHAR(40),
    is_active  BOOLEAN      NOT NULL DEFAULT true,
    sort_order SMALLINT     NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_notif_types (
    id       SMALLSERIAL PRIMARY KEY,
    code     VARCHAR(50)  NOT NULL UNIQUE,
    label    VARCHAR(100) NOT NULL
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_rate_cards (
    id             SMALLSERIAL  PRIMARY KEY,
    skill_cat_id   SMALLINT     NOT NULL REFERENCES kerjantara.ref_skill_categories(id),
    city_code      VARCHAR(20)  NOT NULL DEFAULT 'JAKARTA',
    min_rate       BIGINT       NOT NULL,
    max_rate       BIGINT       NOT NULL,
    rate_unit      VARCHAR(20)  NOT NULL DEFAULT 'per_day',
    is_active      BOOLEAN      NOT NULL DEFAULT true,
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT chk_rate_range CHECK (max_rate > min_rate),
    CONSTRAINT chk_rate_unit  CHECK (rate_unit IN ('per_day', 'per_job', 'per_hour'))
);

-- =========================================================================
-- 3. MASTER TABLES (kerjantara.mst_xxxx) - Entitas Utama
-- =========================================================================

-- -------------------------------------------------------------------------
-- mst_users: Satu akun bisa punya banyak role (worker + employer)
-- role tidak lagi di kolom tunggal — lihat mst_user_roles di bawah
-- -------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS kerjantara.mst_users (
    id               UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
    verif_status_id  SMALLINT     NOT NULL REFERENCES kerjantara.ref_verif_statuses(id),
    full_name        VARCHAR(100) NOT NULL,
    phone            VARCHAR(20)  NOT NULL UNIQUE,
    password_hash    TEXT         NOT NULL,
    ktp_file_key     TEXT,                     -- Path di Supabase Storage, bukan URL langsung
    selfie_file_key  TEXT,
    -- is_active di sini = akun aktif (bukan suspended/deleted)
    -- toggle "siap terima kerja" ada di mst_worker_profiles.is_available
    is_active        BOOLEAN      NOT NULL DEFAULT true,
    location         public.GEOGRAPHY(Point, 4326), -- Koordinat GPS terbaru
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- -------------------------------------------------------------------------
-- mst_user_roles: Junction table user <-> role (many-to-many)
-- Satu user bisa punya role worker DAN employer sekaligus
-- active_role = role yang sedang aktif dipakai (untuk JWT claim)
-- -------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS kerjantara.mst_user_roles (
    id          UUID      PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID      NOT NULL REFERENCES kerjantara.mst_users(id) ON DELETE CASCADE,
    role_id     SMALLINT  NOT NULL REFERENCES kerjantara.ref_user_roles(id),
    is_primary  BOOLEAN   NOT NULL DEFAULT false, -- Role utama saat pertama daftar
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_user_role UNIQUE (user_id, role_id)
);

-- -------------------------------------------------------------------------
-- mst_worker_profiles: Profil khusus pekerja
-- Dibuat otomatis saat user register dengan role worker
-- atau saat user employer menambahkan role worker
-- is_available = toggle "Saya siap terima kerjaan" (berbeda dari is_active di mst_users)
-- -------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS kerjantara.mst_worker_profiles (
    id                  UUID          PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id             UUID          NOT NULL UNIQUE REFERENCES kerjantara.mst_users(id) ON DELETE CASCADE,
    years_experience    SMALLINT      NOT NULL DEFAULT 0,
    kerjantara_score    NUMERIC(4,2)  NOT NULL DEFAULT 0.00
                            CONSTRAINT chk_score_range CHECK (kerjantara_score BETWEEN 0 AND 5),
    total_jobs_done     INT           NOT NULL DEFAULT 0,
    avg_response_min    FLOAT         NOT NULL DEFAULT 0,
    bio                 TEXT,
    is_available        BOOLEAN       NOT NULL DEFAULT false, -- Toggle siap terima kerja
    kitadompet_balance  BIGINT        NOT NULL DEFAULT 0
                            CONSTRAINT chk_balance_non_negative CHECK (kitadompet_balance >= 0),
    updated_at          TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kerjantara.mst_worker_skills (
    id            UUID      PRIMARY KEY DEFAULT uuid_generate_v4(),
    worker_id     UUID      NOT NULL REFERENCES kerjantara.mst_users(id) ON DELETE CASCADE,
    skill_cat_id  SMALLINT  NOT NULL REFERENCES kerjantara.ref_skill_categories(id),
    is_primary    BOOLEAN   NOT NULL DEFAULT false,
    CONSTRAINT uq_worker_skill UNIQUE (worker_id, skill_cat_id)
);

-- =========================================================================
-- 4. TRANSACTION TABLES (kerjantara.trx_xxxx)
-- =========================================================================

CREATE TABLE IF NOT EXISTS kerjantara.trx_jobs (
    id                UUID          PRIMARY KEY DEFAULT uuid_generate_v4(),
    employer_id       UUID          NOT NULL REFERENCES kerjantara.mst_users(id),
    skill_cat_id      SMALLINT      NOT NULL REFERENCES kerjantara.ref_skill_categories(id),
    status_id         SMALLINT      NOT NULL REFERENCES kerjantara.ref_job_statuses(id),
    description       TEXT          NOT NULL,
    budget            BIGINT        NOT NULL CONSTRAINT chk_budget_positive CHECK (budget > 0),
    agreed_price      BIGINT        CONSTRAINT chk_agreed_positive CHECK (agreed_price IS NULL OR agreed_price > 0),
    search_radius_km  FLOAT         NOT NULL DEFAULT 2,
    location          public.GEOGRAPHY(Point, 4326) NOT NULL,
    city_code         VARCHAR(20)   NOT NULL DEFAULT 'JAKARTA',
    posted_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ   NOT NULL,
    price_accepted_at TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    deleted_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS kerjantara.trx_job_matches (
    id                UUID          PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id            UUID          NOT NULL REFERENCES kerjantara.trx_jobs(id),
    worker_id         UUID          NOT NULL REFERENCES kerjantara.mst_users(id),
    composite_score   FLOAT         NOT NULL,
    score_breakdown   JSONB         NOT NULL DEFAULT '{}',
    match_rank        SMALLINT      NOT NULL CONSTRAINT chk_rank CHECK (match_rank BETWEEN 1 AND 3),
    match_status      VARCHAR(20)   NOT NULL DEFAULT 'recommended'
                          CONSTRAINT chk_response CHECK (
                              match_status IN ('recommended', 'accepted', 'rejected', 'timeout')
                          ),
    response_deadline TIMESTAMPTZ,
    notified_at       TIMESTAMPTZ,
    responded_at      TIMESTAMPTZ,
    CONSTRAINT uq_job_worker UNIQUE (job_id, worker_id)
);

CREATE TABLE IF NOT EXISTS kerjantara.trx_ratings (
    id          UUID          PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id      UUID          NOT NULL REFERENCES kerjantara.trx_jobs(id),
    rater_id    UUID          NOT NULL REFERENCES kerjantara.mst_users(id),
    ratee_id    UUID          NOT NULL REFERENCES kerjantara.mst_users(id),
    score       NUMERIC(2,1)  NOT NULL CONSTRAINT chk_score CHECK (score BETWEEN 1 AND 5),
    comment     TEXT,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    CONSTRAINT uq_rating UNIQUE (job_id, rater_id, ratee_id)
);

CREATE TABLE IF NOT EXISTS kerjantara.trx_payments (
    id                   UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id               UUID         NOT NULL UNIQUE REFERENCES kerjantara.trx_jobs(id),
    employer_id          UUID         NOT NULL REFERENCES kerjantara.mst_users(id),
    worker_id            UUID         NOT NULL REFERENCES kerjantara.mst_users(id),
    amount               BIGINT       NOT NULL CONSTRAINT chk_amount_positive CHECK (amount > 0),
    platform_fee         BIGINT       NOT NULL DEFAULT 0
                             CONSTRAINT chk_fee_non_negative CHECK (platform_fee >= 0),
    net_to_worker        BIGINT       NOT NULL CONSTRAINT chk_net_positive CHECK (net_to_worker > 0),
    status               VARCHAR(20)  NOT NULL DEFAULT 'pending'
                             CONSTRAINT chk_payment_status CHECK (
                                 status IN ('pending', 'held', 'released', 'refunded')
                             ),
    midtrans_order_id    VARCHAR(100) UNIQUE,
    midtrans_snap_token  TEXT,
    held_at              TIMESTAMPTZ,
    released_at          TIMESTAMPTZ
);

-- =========================================================================
-- 5. LOG TABLES (kerjantara.log_xxxx) - Audit Trails (Insert-only)
-- =========================================================================

CREATE TABLE IF NOT EXISTS kerjantara.log_job_status_history (
    id             UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id         UUID        NOT NULL REFERENCES kerjantara.trx_jobs(id),
    from_status_id SMALLINT    REFERENCES kerjantara.ref_job_statuses(id),
    to_status_id   SMALLINT    NOT NULL REFERENCES kerjantara.ref_job_statuses(id),
    changed_by     UUID        REFERENCES kerjantara.mst_users(id),
    changed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kerjantara.log_kerjantara_score_history (
    id                 UUID          PRIMARY KEY DEFAULT uuid_generate_v4(),
    worker_id          UUID          NOT NULL REFERENCES kerjantara.mst_users(id),
    score_before       NUMERIC(4,2)  NOT NULL,
    score_after        NUMERIC(4,2)  NOT NULL,
    delta              NUMERIC(4,2)  NOT NULL,
    triggered_by_job   UUID          REFERENCES kerjantara.trx_jobs(id),
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kerjantara.log_payment_events (
    id          UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
    payment_id  UUID         NOT NULL REFERENCES kerjantara.trx_payments(id),
    event_type  VARCHAR(50)  NOT NULL,
    payload     JSONB        NOT NULL DEFAULT '{}',
    received_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kerjantara.log_match_responses (
    id           UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
    match_id     UUID         NOT NULL REFERENCES kerjantara.trx_job_matches(id),
    job_id       UUID         NOT NULL REFERENCES kerjantara.trx_jobs(id),
    worker_id    UUID         NOT NULL REFERENCES kerjantara.mst_users(id),
    response     VARCHAR(20)  NOT NULL
                     CONSTRAINT chk_log_response CHECK (
                         response IN ('accepted', 'rejected', 'timeout')
                     ),
    responded_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- =========================================================================
-- 6. INDEX - Optimasi Performa Query & Algoritma
-- =========================================================================

-- Spasial GIST untuk matching engine (ST_DWithin)
CREATE INDEX IF NOT EXISTS idx_mst_users_location
    ON kerjantara.mst_users USING GIST (location);

CREATE INDEX IF NOT EXISTS idx_trx_jobs_location
    ON kerjantara.trx_jobs USING GIST (location);

-- Auth & lookup
CREATE INDEX IF NOT EXISTS idx_mst_users_phone
    ON kerjantara.mst_users (phone);

-- Dual role queries
CREATE INDEX IF NOT EXISTS idx_mst_user_roles_user
    ON kerjantara.mst_user_roles (user_id);

CREATE INDEX IF NOT EXISTS idx_mst_user_roles_role
    ON kerjantara.mst_user_roles (role_id);

-- Worker availability untuk matching engine filter
CREATE INDEX IF NOT EXISTS idx_mst_worker_profiles_available
    ON kerjantara.mst_worker_profiles (is_available)
    WHERE is_available = true;

-- Skills lookup untuk hard filter
CREATE INDEX IF NOT EXISTS idx_mst_worker_skills_cat
    ON kerjantara.mst_worker_skills (skill_cat_id);

-- Rate card lookup
CREATE INDEX IF NOT EXISTS idx_ref_rate_cards_skill_city
    ON kerjantara.ref_rate_cards (skill_cat_id, city_code);

-- Partial index cron job timeout checker
CREATE INDEX IF NOT EXISTS idx_trx_job_matches_pending_deadline
    ON kerjantara.trx_job_matches (response_deadline)
    WHERE match_status = 'recommended';

-- Audit log indexes
CREATE INDEX IF NOT EXISTS idx_log_job_status_job
    ON kerjantara.log_job_status_history (job_id);

CREATE INDEX IF NOT EXISTS idx_log_score_worker
    ON kerjantara.log_kerjantara_score_history (worker_id);

CREATE INDEX IF NOT EXISTS idx_log_payment_events_payment
    ON kerjantara.log_payment_events (payment_id);

CREATE INDEX IF NOT EXISTS idx_log_match_responses_job
    ON kerjantara.log_match_responses (job_id);

CREATE INDEX IF NOT EXISTS idx_log_match_responses_worker
    ON kerjantara.log_match_responses (worker_id);

-- =========================================================================
-- 7. SEED DATA - ref_ tables
-- =========================================================================

INSERT INTO kerjantara.ref_verif_statuses (code, label, sort_order) VALUES
    ('pending',  'Menunggu Review', 1),
    ('approved', 'Terverifikasi',   2),
    ('rejected', 'Ditolak',         3),
    ('resubmit', 'Upload Ulang',    4)
ON CONFLICT (code) DO NOTHING;

INSERT INTO kerjantara.ref_user_roles (code, label) VALUES
    ('worker',   'Pekerja'),
    ('employer', 'Pemberi Kerja'),
    ('admin',    'Admin'),
    ('reviewer', 'Reviewer KTP')
ON CONFLICT (code) DO NOTHING;

INSERT INTO kerjantara.ref_job_statuses (code, label, sort_order) VALUES
    ('pending',   'Mencari Kandidat',   1),
    ('matched',   'Kandidat Ditemukan', 2),
    ('accepted',  'Pekerja Diterima',   3),
    ('ongoing',   'Sedang Dikerjakan',  4),
    ('done',      'Selesai',            5),
    ('cancelled', 'Dibatalkan',         6),
    ('expired',   'Kedaluwarsa',        7),
    ('no_takers', 'Tidak Ada Peminat',  8)
ON CONFLICT (code) DO NOTHING;

INSERT INTO kerjantara.ref_skill_categories
    (id, parent_id, code, label_id, icon_key, sort_order)
VALUES
    -- Level 1: Grup
    (1,  NULL, 'RUMAH_TANGGA', 'Rumah Tangga',         'home',      1),
    (10, NULL, 'KONSTRUKSI',   'Konstruksi & Renovasi', 'hammer',    2),
    (20, NULL, 'TRANSPORTASI', 'Transportasi',          'car',       3),
    (30, NULL, 'LAINNYA',      'Lainnya',               'ellipsis',  4),
    -- Level 2: Rumah Tangga
    (2,  1,  'ART_HARIAN',       'ART Harian',        'home',        1),
    (3,  1,  'CLEANING_SERVICE', 'Cleaning Service',  'sparkles',    2),
    (4,  1,  'JAGA_ANAK',        'Penjaga Anak',      'baby',        3),
    (5,  1,  'JAGA_LANSIA',      'Penjaga Lansia',    'heart',       4),
    -- Level 2: Konstruksi
    (11, 10, 'TUKANG_CAT',       'Tukang Cat',        'paintbrush',  1),
    (12, 10, 'TUKANG_BANGUNAN',  'Tukang Bangunan',   'brick',       2),
    (13, 10, 'TUKANG_LISTRIK',   'Tukang Listrik',    'zap',         3),
    (14, 10, 'TUKANG_LEDENG',    'Tukang Ledeng',     'droplets',    4),
    -- Level 2: Transportasi
    (21, 20, 'SUPIR_PRIBADI',    'Supir Pribadi',     'steering',    1),
    (22, 20, 'SUPIR_HARIAN',     'Supir Harian',      'car',         2),
    -- Level 2: Lainnya
    (31, 30, 'KURIR_LOKAL',      'Kurir Lokal',       'package',     1),
    (32, 30, 'FOTOGRAFER',       'Fotografer',        'camera',      2)
ON CONFLICT (id) DO NOTHING;

INSERT INTO kerjantara.ref_notif_types (code, label) VALUES
    ('job_request',      'Permintaan Kerja Baru'),
    ('job_accepted',     'Pekerja Menerima Job'),
    ('job_rejected',     'Pekerja Menolak Job'),
    ('job_arrived',      'Pekerja Sudah Sampai'),
    ('job_done',         'Pekerjaan Selesai'),
    ('payment_released', 'Dana Diterima'),
    ('rating_reminder',  'Jangan Lupa Beri Rating'),
    ('ktp_review',       'KTP Baru Perlu Direview'),
    ('job_expired',      'Job Kedaluwarsa - Naikkan Budget'),
    ('no_takers',        'Tidak Ada Pekerja yang Menerima')
ON CONFLICT (code) DO NOTHING;

INSERT INTO kerjantara.ref_rate_cards
    (skill_cat_id, city_code, min_rate, max_rate, rate_unit)
VALUES
    -- Jakarta
    (2,  'JAKARTA', 100000,  300000,  'per_day'),
    (3,  'JAKARTA', 150000,  350000,  'per_day'),
    (4,  'JAKARTA', 150000,  400000,  'per_day'),
    (5,  'JAKARTA', 150000,  400000,  'per_day'),
    (11, 'JAKARTA', 150000,  400000,  'per_day'),
    (12, 'JAKARTA', 200000,  500000,  'per_day'),
    (13, 'JAKARTA', 200000,  500000,  'per_day'),
    (14, 'JAKARTA', 150000,  350000,  'per_day'),
    (21, 'JAKARTA', 200000,  500000,  'per_day'),
    (22, 'JAKARTA', 150000,  400000,  'per_day'),
    (31, 'JAKARTA',  50000,  150000,  'per_job'),
    (32, 'JAKARTA', 300000, 1000000,  'per_job'),
    -- Bodetabek
    (2,  'BODETABEK',  80000, 250000, 'per_day'),
    (3,  'BODETABEK', 100000, 300000, 'per_day'),
    (11, 'BODETABEK', 120000, 350000, 'per_day'),
    (12, 'BODETABEK', 150000, 450000, 'per_day'),
    (21, 'BODETABEK', 150000, 400000, 'per_day'),
    (22, 'BODETABEK', 125000, 350000, 'per_day')
ON CONFLICT DO NOTHING;

-- =========================================================================
-- 8. CATATAN PENTING UNTUK DEVELOPER
-- =========================================================================

-- DUAL ROLE FLOW:
-- Saat register sebagai worker:
--   INSERT mst_users → INSERT mst_user_roles (role=worker, is_primary=true)
--   → INSERT mst_worker_profiles (otomatis)
--
-- Saat register sebagai employer:
--   INSERT mst_users → INSERT mst_user_roles (role=employer, is_primary=true)
--   (tidak ada worker_profile)
--
-- Saat user employer tambah role worker:
--   INSERT mst_user_roles (role=worker, is_primary=false)
--   → INSERT mst_worker_profiles (otomatis)
--
-- Saat switch mode di app (mirip Gojek):
--   FE redirect ke /dashboard/worker atau /dashboard/employer
--   JWT claim tetap berisi semua roles user (array)
--   BE validasi role dari JWT sesuai endpoint yang dipanggil

-- MATCHING ENGINE — perubahan dari single role:
-- Hard filter sekarang join ke mst_user_roles + mst_worker_profiles:
--   WHERE EXISTS (
--       SELECT 1 FROM kerjantara.mst_user_roles ur
--       JOIN kerjantara.ref_user_roles r ON ur.role_id = r.id
--       WHERE ur.user_id = u.id AND r.code = 'worker'
--   )
--   AND wp.is_available = true   ← ganti dari mst_users.is_active
--   AND u.verif_status_id = (SELECT id FROM ref_verif_statuses WHERE code='approved')

-- IS_ACTIVE vs IS_AVAILABLE:
--   mst_users.is_active       = akun aktif / tidak suspended (default true)
--   mst_worker_profiles.is_available = toggle siap terima kerja (default false)
--   Matching engine filter by is_available, BUKAN is_active

-- JWT PAYLOAD yang disarankan:
-- {
--   "sub": "uuid-user",
--   "roles": ["worker", "employer"],   ← array semua role yang dimiliki
--   "primary_role": "employer",
--   "verif_status": "approved",
--   "exp": 1234567890
-- }
