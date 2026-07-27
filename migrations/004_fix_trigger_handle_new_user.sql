-- =========================================================================
-- FILE: 004_fix_trigger_handle_new_user.sql
-- DESKRIPSI: Perbaiki trigger handle_new_user agar auto-create roles + worker profile
-- PETUNJUK:  Jalankan via Supabase SQL Editor
-- =========================================================================

-- 1. Drop trigger lama & function
DROP TRIGGER IF EXISTS on_auth_user_created ON auth.users;
DROP FUNCTION IF EXISTS kerjantara.handle_new_user();

-- 2. Buat ulang function yang lengkap
CREATE OR REPLACE FUNCTION kerjantara.handle_new_user()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
AS $$
DECLARE
    v_pending_verif_id SMALLINT;
    v_worker_role_id   SMALLINT;
    v_user_phone       VARCHAR(20);
BEGIN
    -- Resolve phone: ambil dari auth.users.phone atau user_metadata
    v_user_phone := COALESCE(
        NEW.phone,
        NEW.raw_user_meta_data ->> 'phone_number',
        NEW.raw_user_meta_data ->> 'phone',
        COALESCE(NEW.email, 'google_' || LEFT(NEW.id::text, 12))
    );

    -- Ambil ref IDs
    SELECT id INTO v_pending_verif_id FROM kerjantara.ref_verif_statuses WHERE code = 'pending';
    SELECT id INTO v_worker_role_id   FROM kerjantara.ref_user_roles      WHERE code = 'worker';

    -- Insert ke mst_users (ON CONFLICT jaga-jaga kalau sudah ada)
    INSERT INTO kerjantara.mst_users (id, verif_status_id, full_name, phone, is_active)
    VALUES (
        NEW.id,
        v_pending_verif_id,
        COALESCE(NEW.raw_user_meta_data ->> 'full_name', NEW.raw_user_meta_data ->> 'name', 'Pengguna Baru'),
        v_user_phone,
        true
    )
    ON CONFLICT (id) DO NOTHING;

    -- Insert worker role
    INSERT INTO kerjantara.mst_user_roles (user_id, role_id, is_primary)
    VALUES (NEW.id, v_worker_role_id, true)
    ON CONFLICT (user_id, role_id) DO NOTHING;

    -- Insert worker profile (lazy)
    INSERT INTO kerjantara.mst_worker_profiles (user_id)
    VALUES (NEW.id)
    ON CONFLICT (user_id) DO NOTHING;

    RETURN NEW;
END;
$$;

-- 3. Pasang ulang trigger
CREATE TRIGGER on_auth_user_created
    AFTER INSERT ON auth.users
    FOR EACH ROW
    EXECUTE FUNCTION kerjantara.handle_new_user();

-- 4. Fix existing users yang tidak punya role / worker profile (dari trigger lama)
INSERT INTO kerjantara.mst_user_roles (user_id, role_id, is_primary)
SELECT mu.id, (SELECT id FROM kerjantara.ref_user_roles WHERE code = 'worker'), true
FROM kerjantara.mst_users mu
WHERE NOT EXISTS (
    SELECT 1 FROM kerjantara.mst_user_roles mur WHERE mur.user_id = mu.id
)
ON CONFLICT (user_id, role_id) DO NOTHING;

INSERT INTO kerjantara.mst_worker_profiles (user_id)
SELECT mu.id
FROM kerjantara.mst_users mu
WHERE NOT EXISTS (
    SELECT 1 FROM kerjantara.mst_worker_profiles wp WHERE wp.user_id = mu.id
)
ON CONFLICT (user_id) DO NOTHING;

-- Verifikasi
SELECT mu.full_name, array_agg(r.code) AS roles, wp.is_available IS NOT NULL AS has_profile
FROM kerjantara.mst_users mu
LEFT JOIN kerjantara.mst_user_roles mur ON mur.user_id = mu.id
LEFT JOIN kerjantara.ref_user_roles r ON r.id = mur.role_id
LEFT JOIN kerjantara.mst_worker_profiles wp ON wp.user_id = mu.id
GROUP BY mu.id, mu.full_name, wp.is_available;
-- Semua user harus punya role 'worker' dan worker profile
