-- ============================================================
-- SEEDER — Update 2 user existing jadi Worker + Employer
-- UUID dari hasil query user yang ada di auth.users
-- ============================================================

-- ============================================================
-- STEP 1: Fix verif_status jadi approved untuk semua user
-- ============================================================
UPDATE kerjantara.mst_users
SET verif_status_id = (SELECT id FROM kerjantara.ref_verif_statuses WHERE code = 'approved')
WHERE id IN ('8f553870-fc6e-4838-83a2-b561db7a3e3d', 'ddc970d2-290c-4201-ab51-c259c24132fd');

-- ============================================================
-- STEP 2: "Team Kerjantara" — jadi Worker + Employer
--    UUID: 8f553870-fc6e-4838-83a2-b561db7a3e3d
-- ============================================================

-- Role employer (kalau belum ada)
INSERT INTO kerjantara.mst_user_roles (user_id, role_id, is_primary)
VALUES ('8f553870-fc6e-4838-83a2-b561db7a3e3d', 2, true)  -- employer
ON CONFLICT (user_id, role_id) DO NOTHING;

-- Role worker (kalau belum ada)
INSERT INTO kerjantara.mst_user_roles (user_id, role_id, is_primary)
VALUES ('8f553870-fc6e-4838-83a2-b561db7a3e3d', 1, false)  -- worker
ON CONFLICT (user_id, role_id) DO NOTHING;

-- Worker profile + lokasi di Jakarta Timur (dekat job)
INSERT INTO kerjantara.mst_worker_profiles (user_id, is_available, kerjantara_score, total_jobs_done, avg_response_min, bio)
VALUES ('8f553870-fc6e-4838-83a2-b561db7a3e3d', true, 4.5, 20, 5.0,
    'Siap kerja ART Harian, area Jakarta Timur')
ON CONFLICT (user_id) DO UPDATE SET is_available = true;

-- Skill ART Harian
INSERT INTO kerjantara.mst_worker_skills (worker_id, skill_cat_id, is_primary)
VALUES ('8f553870-fc6e-4838-83a2-b561db7a3e3d', 2, true)
ON CONFLICT (worker_id, skill_cat_id) DO NOTHING;

-- Lokasi ~300m dari job koordinat (-6.205557, 106.876749)
UPDATE kerjantara.mst_users
SET location = ST_SetSRID(ST_MakePoint(106.873500, -6.203500), 4326)::geography
WHERE id = '8f553870-fc6e-4838-83a2-b561db7a3e3d';

-- ============================================================
-- STEP 3: "Zulham S" — jadi Worker (ART Harian)
--    UUID: ddc970d2-290c-4201-ab51-c259c24132fd
-- ============================================================

-- Role worker
INSERT INTO kerjantara.mst_user_roles (user_id, role_id, is_primary)
VALUES ('ddc970d2-290c-4201-ab51-c259c24132fd', 1, true)  -- worker
ON CONFLICT (user_id, role_id) DO NOTHING;

-- Worker profile + lokasi di Jakarta Timur (~800m dari job)
INSERT INTO kerjantara.mst_worker_profiles (user_id, is_available, kerjantara_score, total_jobs_done, avg_response_min, bio)
VALUES ('ddc970d2-290c-4201-ab51-c259c24132fd', true, 4.1, 8, 12.0,
    'ART harian, bisa masak dan bersih-bersih')
ON CONFLICT (user_id) DO UPDATE SET is_available = true;

-- Skill ART Harian + Cleaning Service
INSERT INTO kerjantara.mst_worker_skills (worker_id, skill_cat_id, is_primary)
VALUES ('ddc970d2-290c-4201-ab51-c259c24132fd', 2, true)
ON CONFLICT (worker_id, skill_cat_id) DO NOTHING;

INSERT INTO kerjantara.mst_worker_skills (worker_id, skill_cat_id, is_primary)
VALUES ('ddc970d2-290c-4201-ab51-c259c24132fd', 3, false)
ON CONFLICT (worker_id, skill_cat_id) DO NOTHING;

-- Lokasi ~800m dari job
UPDATE kerjantara.mst_users
SET location = ST_SetSRID(ST_MakePoint(106.870000, -6.210200), 4326)::geography
WHERE id = 'ddc970d2-290c-4201-ab51-c259c24132fd';

-- ============================================================
-- STEP 4: VERIFIKASI
-- ============================================================
SELECT
    u.id,
    u.full_name,
    vs.code AS verif_status,
    COALESCE(wp.is_available, false) AS available,
    COALESCE(wp.kerjantara_score, 0) AS score,
    COALESCE(wp.total_jobs_done, 0) AS jobs_done,
    string_agg(DISTINCT sc.label_id, ', ') AS skills,
    round(ST_Y(u.location::geometry)::numeric, 4) AS lat,
    round(ST_X(u.location::geometry)::numeric, 4) AS lng
FROM kerjantara.mst_users u
JOIN kerjantara.ref_verif_statuses vs ON u.verif_status_id = vs.id
JOIN kerjantara.mst_user_roles ur ON ur.user_id = u.id
LEFT JOIN kerjantara.mst_worker_profiles wp ON wp.user_id = u.id
LEFT JOIN kerjantara.mst_worker_skills ws ON ws.worker_id = u.id
LEFT JOIN kerjantara.ref_skill_categories sc ON ws.skill_cat_id = sc.id
WHERE u.id IN ('8f553870-fc6e-4838-83a2-b561db7a3e3d', 'ddc970d2-290c-4201-ab51-c259c24132fd')
GROUP BY u.id, u.full_name, vs.code, wp.is_available, wp.kerjantara_score, wp.total_jobs_done
ORDER BY u.full_name;

-- ============================================================
-- Hasil yang diharapkan:
-- Team Kerjantara | approved | true | 4.5 | ART Harian | -6.2035, 106.8735
-- Zulham S        | approved | true | 4.1 | ART Harian, Cleaning Service | -6.2102, 106.8700
--
-- Lalu login sebagai Team Kerjantara (employer),
-- switch role ke employer via PATCH /auth/roles/switch,
-- POST /jobs dengan skill_cat_id=2 → harusnya dapat 1 kandidat (Zulham)
-- (Team Kerjantara di-exclude karena matching engine filter worker_id != employer_id)
-- ============================================================
