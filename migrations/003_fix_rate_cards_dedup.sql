-- =========================================================================
-- FILE: 003_fix_rate_cards_dedup.sql
-- DESKRIPSI: Bersihkan duplikasi ref_rate_cards & tambah UNIQUE constraint
-- PETUNJUK:  Jalankan via Supabase SQL Editor atau psql
-- =========================================================================

-- 1. Hapus data duplikat (simpan ID terkecil per skill_cat_id + city_code)
DELETE FROM kerjantara.ref_rate_cards
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (PARTITION BY skill_cat_id, city_code ORDER BY id) AS rn
        FROM kerjantara.ref_rate_cards
    ) sub
    WHERE rn > 1
);

-- 2. Tambah UNIQUE constraint untuk mencegah duplikasi di masa depan
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'uq_rate_card'
          AND connamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'kerjantara')
    ) THEN
        ALTER TABLE kerjantara.ref_rate_cards
            ADD CONSTRAINT uq_rate_card UNIQUE (skill_cat_id, city_code);
    END IF;
END $$;

-- Verifikasi
SELECT COUNT(*) AS duplicate_count
FROM (
    SELECT skill_cat_id, city_code, COUNT(*) AS cnt
    FROM kerjantara.ref_rate_cards
    GROUP BY skill_cat_id, city_code
    HAVING COUNT(*) > 1
) dupes;
-- Harus return 0
