-- =========================================================================
-- FILE: kerjantara_schema_dump.sql
-- DESKRIPSI: DDL Skema Penuh untuk Kerjantara.id v3.3 — gabungan 001+002
-- DIGUNAKAN UNTUK: Recreate schema di environment non-Supabase
-- TANGGAL DUMP: 2026-07-27
-- =========================================================================

CREATE SCHEMA IF NOT EXISTS kerjantara;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS postgis;

-- =========================================================================
-- REFERENCE TABLES
-- =========================================================================

CREATE TABLE IF NOT EXISTS kerjantara.ref_verif_statuses (
    id         SMALLSERIAL PRIMARY KEY,
    code       VARCHAR(20)  NOT NULL UNIQUE,
    label      VARCHAR(80)  NOT NULL,
    sort_order SMALLINT     DEFAULT 0
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_user_roles (
    id       SMALLSERIAL PRIMARY KEY,
    code     VARCHAR(20)  NOT NULL UNIQUE,
    label    VARCHAR(80)  NOT NULL
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_job_statuses (
    id         SMALLSERIAL PRIMARY KEY,
    code       VARCHAR(25)  NOT NULL UNIQUE,
    label      VARCHAR(80)  NOT NULL,
    sort_order SMALLINT     DEFAULT 0
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_skill_categories (
    id         SMALLINT     PRIMARY KEY,
    parent_id  SMALLINT     REFERENCES kerjantara.ref_skill_categories(id),
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
    CONSTRAINT chk_rate_unit  CHECK (rate_unit IN ('per_day','per_job','per_hour')),
    CONSTRAINT uq_rate_card UNIQUE (skill_cat_id, city_code)
);

CREATE TABLE IF NOT EXISTS kerjantara.ref_cities (
    id         SMALLINT      PRIMARY KEY,
    name       VARCHAR(60)   NOT NULL,
    centroid   public.GEOGRAPHY(Point, 4326),
    is_active  BOOLEAN       NOT NULL DEFAULT true
);

-- =========================================================================
-- MASTER TABLES
-- =========================================================================

CREATE TABLE IF NOT EXISTS kerjantara.mst_users (
    id               UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
    verif_status_id  SMALLINT     NOT NULL REFERENCES kerjantara.ref_verif_statuses(id),
    full_name        VARCHAR(100) NOT NULL,
    phone            VARCHAR(20)  NOT NULL UNIQUE,
    password_hash    TEXT         NOT NULL,
    ktp_file_key     TEXT,
    selfie_file_key  TEXT,
    is_active        BOOLEAN      NOT NULL DEFAULT true,
    location         public.GEOGRAPHY(Point, 4326),
    city_id          SMALLINT     REFERENCES kerjantara.ref_cities(id) ON DELETE SET NULL,
    active_role      VARCHAR(20)  NOT NULL DEFAULT 'worker',
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kerjantara.mst_user_roles (
    id          UUID      PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID      NOT NULL REFERENCES kerjantara.mst_users(id) ON DELETE CASCADE,
    role_id     SMALLINT  NOT NULL REFERENCES kerjantara.ref_user_roles(id),
    is_primary  BOOLEAN   NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_user_role UNIQUE (user_id, role_id)
);

CREATE TABLE IF NOT EXISTS kerjantara.mst_worker_profiles (
    id                  UUID          PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id             UUID          NOT NULL UNIQUE REFERENCES kerjantara.mst_users(id) ON DELETE CASCADE,
    years_experience    SMALLINT      NOT NULL DEFAULT 0,
    kerjantara_score    NUMERIC(4,2)  NOT NULL DEFAULT 0.00
                            CONSTRAINT chk_score_range CHECK (kerjantara_score BETWEEN 0 AND 5),
    total_jobs_done     INT           NOT NULL DEFAULT 0,
    avg_response_min    FLOAT         NOT NULL DEFAULT 0,
    bio                 TEXT,
    is_available        BOOLEAN       NOT NULL DEFAULT false,
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
-- TRANSACTION TABLES
-- =========================================================================

CREATE TABLE IF NOT EXISTS kerjantara.trx_jobs (
    id                  UUID          PRIMARY KEY DEFAULT uuid_generate_v4(),
    employer_id         UUID          NOT NULL REFERENCES kerjantara.mst_users(id),
    skill_cat_id        SMALLINT      NOT NULL REFERENCES kerjantara.ref_skill_categories(id),
    status_id           SMALLINT      NOT NULL REFERENCES kerjantara.ref_job_statuses(id),
    description         TEXT          NOT NULL,
    budget              BIGINT        NOT NULL CONSTRAINT chk_budget_positive CHECK (budget > 0),
    agreed_price        BIGINT        CONSTRAINT chk_agreed_positive CHECK (agreed_price IS NULL OR agreed_price > 0),
    search_radius_km    FLOAT         NOT NULL DEFAULT 2,
    location            public.GEOGRAPHY(Point, 4326) NOT NULL,
    city_code           VARCHAR(20)   NOT NULL DEFAULT 'JAKARTA',
    duration_days       SMALLINT      NOT NULL DEFAULT 1,
    scheduled_start_date DATE         NOT NULL DEFAULT CURRENT_DATE,
    proof_file_keys     TEXT[],
    proof_notes         TEXT,
    posted_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ   NOT NULL,
    price_accepted_at   TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    deleted_at          TIMESTAMPTZ
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
                              match_status IN ('recommended','accepted','rejected','timeout')),
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
                                 status IN ('pending','held','released','refunded')),
    midtrans_order_id    VARCHAR(100) UNIQUE,
    midtrans_snap_token  TEXT,
    held_at              TIMESTAMPTZ,
    released_at          TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS kerjantara.trx_payment_milestones (
    id           UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
    payment_id   UUID         NOT NULL REFERENCES kerjantara.trx_payments(id) ON DELETE CASCADE,
    day_number   SMALLINT     NOT NULL,
    amount       BIGINT       NOT NULL CONSTRAINT chk_milestone_amount CHECK (amount > 0),
    status       VARCHAR(20)  NOT NULL DEFAULT 'pending'
                     CONSTRAINT chk_milestone_status CHECK (
                         status IN ('pending','released','refunded')),
    confirmed_by UUID         REFERENCES kerjantara.mst_users(id),
    released_at  TIMESTAMPTZ,
    CONSTRAINT uq_milestone_day UNIQUE (payment_id, day_number)
);

CREATE TABLE IF NOT EXISTS kerjantara.trx_job_day_logs (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id           UUID NOT NULL REFERENCES kerjantara.trx_jobs(id),
    day_number       SMALLINT NOT NULL,
    proof_file_keys  TEXT[],
    proof_notes      TEXT,
    completed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_by     UUID REFERENCES kerjantara.mst_users(id),
    confirmed_at     TIMESTAMPTZ,
    UNIQUE(job_id, day_number)
);

-- =========================================================================
-- LOG TABLES
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
                         response IN ('accepted','rejected','timeout')),
    responded_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- =========================================================================
-- INDEXES
-- =========================================================================

CREATE INDEX IF NOT EXISTS idx_mst_users_location          ON kerjantara.mst_users USING GIST (location);
CREATE INDEX IF NOT EXISTS idx_trx_jobs_location            ON kerjantara.trx_jobs USING GIST (location);
CREATE INDEX IF NOT EXISTS idx_mst_users_phone              ON kerjantara.mst_users (phone);
CREATE INDEX IF NOT EXISTS idx_mst_user_roles_user          ON kerjantara.mst_user_roles (user_id);
CREATE INDEX IF NOT EXISTS idx_mst_user_roles_role          ON kerjantara.mst_user_roles (role_id);
CREATE INDEX IF NOT EXISTS idx_mst_worker_profiles_available ON kerjantara.mst_worker_profiles (is_available) WHERE is_available = true;
CREATE INDEX IF NOT EXISTS idx_mst_worker_skills_cat        ON kerjantara.mst_worker_skills (skill_cat_id);
CREATE INDEX IF NOT EXISTS idx_ref_rate_cards_skill_city    ON kerjantara.ref_rate_cards (skill_cat_id, city_code);
CREATE INDEX IF NOT EXISTS idx_trx_job_matches_pending_deadline ON kerjantara.trx_job_matches (response_deadline) WHERE match_status = 'recommended';
CREATE INDEX IF NOT EXISTS idx_log_job_status_job           ON kerjantara.log_job_status_history (job_id);
CREATE INDEX IF NOT EXISTS idx_log_score_worker             ON kerjantara.log_kerjantara_score_history (worker_id);
CREATE INDEX IF NOT EXISTS idx_log_payment_events_payment   ON kerjantara.log_payment_events (payment_id);
CREATE INDEX IF NOT EXISTS idx_log_match_responses_job      ON kerjantara.log_match_responses (job_id);
CREATE INDEX IF NOT EXISTS idx_log_match_responses_worker   ON kerjantara.log_match_responses (worker_id);
CREATE INDEX IF NOT EXISTS idx_mst_users_city               ON kerjantara.mst_users (city_id);
CREATE INDEX IF NOT EXISTS idx_trx_jobs_scheduled_date      ON kerjantara.trx_jobs (scheduled_start_date);
CREATE INDEX IF NOT EXISTS idx_milestones_payment           ON kerjantara.trx_payment_milestones (payment_id);

-- =========================================================================
-- SEED DATA
-- =========================================================================

INSERT INTO kerjantara.ref_verif_statuses (code, label, sort_order) VALUES
    ('newuser',  'User Baru', 0),
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
    ('pending',                'Mencari Kandidat',   1),
    ('matched',                'Kandidat Ditemukan', 2),
    ('accepted',               'Pekerja Diterima',   3),
    ('ongoing',                'Sedang Dikerjakan',  4),
    ('done',                   'Selesai',            5),
    ('cancelled',              'Dibatalkan',         6),
    ('expired',                'Kedaluwarsa',        7),
    ('no_takers',              'Tidak Ada Peminat',  8),
    ('pending_city_fallback',  'Menunggu Pilihan Kota', 9)
ON CONFLICT (code) DO NOTHING;

INSERT INTO kerjantara.ref_skill_categories (id, parent_id, code, label_id, icon_key, sort_order) VALUES
    (1,  NULL, 'RUMAH_TANGGA',   'Rumah Tangga',         'home',      1),
    (2,  1,    'ART_HARIAN',       'ART Harian',        'home',        1),
    (3,  1,    'CLEANING_SERVICE', 'Cleaning Service',  'sparkles',    2),
    (4,  1,    'JAGA_ANAK',        'Penjaga Anak',      'baby',        3),
    (5,  1,    'JAGA_LANSIA',      'Penjaga Lansia',    'heart',       4),
    (10, NULL, 'KONSTRUKSI',     'Konstruksi & Renovasi', 'hammer',    2),
    (11, 10,   'TUKANG_CAT',       'Tukang Cat',        'paintbrush',  1),
    (12, 10,   'TUKANG_BANGUNAN',  'Tukang Bangunan',   'brick',       2),
    (13, 10,   'TUKANG_LISTRIK',   'Tukang Listrik',    'zap',         3),
    (14, 10,   'TUKANG_LEDENG',    'Tukang Ledeng',     'droplets',    4),
    (20, NULL, 'TRANSPORTASI',   'Transportasi',          'car',       3),
    (21, 20,   'SUPIR_PRIBADI',    'Supir Pribadi',     'steering',    1),
    (22, 20,   'SUPIR_HARIAN',     'Supir Harian',      'car',         2),
    (30, NULL, 'LAINNYA',        'Lainnya',               'ellipsis',  4),
    (31, 30,   'KURIR_LOKAL',      'Kurir Lokal',       'package',     1),
    (32, 30,   'FOTOGRAFER',       'Fotografer',        'camera',      2)
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

INSERT INTO kerjantara.ref_rate_cards (skill_cat_id, city_code, min_rate, max_rate, rate_unit) VALUES
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
    (4,  'BODETABEK', 120000, 350000, 'per_day'),
    (5,  'BODETABEK', 120000, 350000, 'per_day'),
    (11, 'BODETABEK', 120000, 350000, 'per_day'),
    (12, 'BODETABEK', 150000, 450000, 'per_day'),
    (13, 'BODETABEK', 150000, 450000, 'per_day'),
    (14, 'BODETABEK', 120000, 300000, 'per_day'),
    (21, 'BODETABEK', 150000, 400000, 'per_day'),
    (22, 'BODETABEK', 125000, 350000, 'per_day')
ON CONFLICT DO NOTHING;

INSERT INTO kerjantara.ref_cities (id, name, centroid) VALUES
    (1, 'Jakarta Selatan', ST_SetSRID(ST_MakePoint(106.8456, -6.2088), 4326)::geography),
    (2, 'Jakarta Pusat',   ST_SetSRID(ST_MakePoint(106.8225, -6.1865), 4326)::geography),
    (3, 'Jakarta Timur',   ST_SetSRID(ST_MakePoint(106.8866, -6.2244), 4326)::geography),
    (4, 'Jakarta Barat',   ST_SetSRID(ST_MakePoint(106.7581, -6.1683), 4326)::geography),
    (5, 'Jakarta Utara',   ST_SetSRID(ST_MakePoint(106.8765, -6.1379), 4326)::geography),
    (6, 'Depok',           ST_SetSRID(ST_MakePoint(106.8186, -6.4025), 4326)::geography),
    (7, 'Bogor',           ST_SetSRID(ST_MakePoint(106.8060, -6.5971), 4326)::geography),
    (8, 'Tangerang',       ST_SetSRID(ST_MakePoint(106.6305, -6.1781), 4326)::geography),
    (9, 'Bekasi',          ST_SetSRID(ST_MakePoint(106.9896, -6.2349), 4326)::geography)
ON CONFLICT (id) DO NOTHING;
