-- =========================================================================
-- FILE: 002_missing_schema_fix.sql
-- DESKRIPSI: DDL tambahan untuk menyelaraskan schema dengan Arsitektur v3.3
--             dan matching engine yang sudah diimplementasi di kode.
-- PETUNJUK:  Jalankan seluruh script ini via Supabase SQL Editor.
-- =========================================================================

-- =========================================================================
-- 1. REF_CITIES — daftar kota untuk fallback matching (Section 7.4.2)
-- =========================================================================
CREATE TABLE IF NOT EXISTS kerjantara.ref_cities (
    id         SMALLINT      PRIMARY KEY,
    name       VARCHAR(60)   NOT NULL,
    centroid   public.GEOGRAPHY(Point, 4326),
    is_active  BOOLEAN       NOT NULL DEFAULT true
);

-- Seed kota pilot (Jakarta + Bodetabek)
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

-- =========================================================================
-- 2. MST_USERS — tambah kolom city_id (Section 7.4.2)
-- =========================================================================
ALTER TABLE kerjantara.mst_users
    ADD COLUMN IF NOT EXISTS city_id SMALLINT
        REFERENCES kerjantara.ref_cities(id) ON DELETE SET NULL;

-- =========================================================================
-- 3. TRX_JOBS — tambah kolom untuk multi-day job & proof upload
-- =========================================================================

-- duration_days: default 1 = job harian standar (Section 8.3)
ALTER TABLE kerjantara.trx_jobs
    ADD COLUMN IF NOT EXISTS duration_days SMALLINT NOT NULL DEFAULT 1;

-- scheduled_start_date: untuk overlap check jadwal pekerja (Section 7.1.2)
ALTER TABLE kerjantara.trx_jobs
    ADD COLUMN IF NOT EXISTS scheduled_start_date DATE NOT NULL DEFAULT CURRENT_DATE;

-- proof_file_keys & proof_notes: untuk upload bukti kerja (API Contract)
ALTER TABLE kerjantara.trx_jobs
    ADD COLUMN IF NOT EXISTS proof_file_keys TEXT[];

ALTER TABLE kerjantara.trx_jobs
    ADD COLUMN IF NOT EXISTS proof_notes TEXT;

-- =========================================================================
-- 4. TRX_PAYMENT_MILESTONES — rilis dana harian (Section 7.3 & 8.3)
-- =========================================================================
CREATE TABLE IF NOT EXISTS kerjantara.trx_payment_milestones (
    id           UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
    payment_id   UUID         NOT NULL REFERENCES kerjantara.trx_payments(id) ON DELETE CASCADE,
    day_number   SMALLINT     NOT NULL,
    amount       BIGINT       NOT NULL CONSTRAINT chk_milestone_amount CHECK (amount > 0),
    status       VARCHAR(20)  NOT NULL DEFAULT 'pending'
                     CONSTRAINT chk_milestone_status CHECK (
                         status IN ('pending', 'released', 'refunded')
                     ),
    confirmed_by UUID         REFERENCES kerjantara.mst_users(id),
    released_at  TIMESTAMPTZ,
    CONSTRAINT uq_milestone_day UNIQUE (payment_id, day_number)
);

CREATE INDEX IF NOT EXISTS idx_milestones_payment
    ON kerjantara.trx_payment_milestones (payment_id);

-- =========================================================================
-- 5. REF_JOB_STATUSES — perluas kolom code (22 char > 20) & seed city fallback
-- =========================================================================
ALTER TABLE kerjantara.ref_job_statuses
    ALTER COLUMN code TYPE VARCHAR(25);

INSERT INTO kerjantara.ref_job_statuses (code, label, sort_order) VALUES
    ('pending_city_fallback', 'Menunggu Pilihan Kota', 9)
ON CONFLICT (code) DO NOTHING;

-- =========================================================================
-- 6. INDEX tambahan
-- =========================================================================
CREATE INDEX IF NOT EXISTS idx_mst_users_city
    ON kerjantara.mst_users (city_id);

CREATE INDEX IF NOT EXISTS idx_trx_jobs_scheduled_date
    ON kerjantara.trx_jobs (scheduled_start_date);

-- =========================================================================
-- 7. REF_RATE_CARDS — seed untuk skill tambahan yang belum ada
-- =========================================================================
INSERT INTO kerjantara.ref_rate_cards (skill_cat_id, city_code, min_rate, max_rate, rate_unit) VALUES
    (4,  'BODETABEK', 120000, 350000, 'per_day'),
    (5,  'BODETABEK', 120000, 350000, 'per_day'),
    (13, 'BODETABEK', 150000, 450000, 'per_day'),
    (14, 'BODETABEK', 120000, 300000, 'per_day')
ON CONFLICT DO NOTHING;

-- =========================================================================
-- VERIFIKASI: jalankan query ini setelah eksekusi untuk memastikan semua OK
-- =========================================================================
/*
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = 'kerjantara'
  AND table_name = 'trx_jobs'
  AND column_name IN ('duration_days', 'scheduled_start_date', 'proof_file_keys', 'proof_notes');
-- Harus return 4 rows

SELECT * FROM kerjantara.ref_cities;
-- Harus return 9 rows (Jakarta Selatan s.d. Bekasi)

SELECT * FROM kerjantara.ref_job_statuses WHERE code = 'pending_city_fallback';
-- Harus return 1 row

SELECT table_name FROM information_schema.tables
WHERE table_schema = 'kerjantara' AND table_name = 'trx_payment_milestones';
-- Harus return 1 row

SELECT column_name FROM information_schema.columns
WHERE table_schema = 'kerjantara'
  AND table_name = 'mst_users'
  AND column_name = 'city_id';
-- Harus return 1 row
*/
