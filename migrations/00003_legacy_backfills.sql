-- Frozen replacement for the former startup verification/account backfills.
-- Every statement runs in the goose transaction; errors contain no identifiers or PII.
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM driver_profiles WHERE licence_number <> ''
    GROUP BY UPPER(REPLACE(REPLACE(TRIM(licence_number), ' ', ''), '-', '')) HAVING COUNT(*) > 1
  ) OR EXISTS (
    SELECT 1 FROM driver_profiles WHERE plate_number <> ''
    GROUP BY UPPER(REPLACE(REPLACE(TRIM(plate_number), ' ', ''), '-', '')) HAVING COUNT(*) > 1
  ) THEN
    RAISE EXCEPTION 'Resolve duplicate driver identity values before migration; values are redacted';
  END IF;
END $$;
-- +goose StatementEnd

UPDATE driver_profiles SET licence_number = UPPER(REPLACE(REPLACE(TRIM(licence_number), ' ', ''), '-', ''));
UPDATE driver_profiles SET plate_number = UPPER(REPLACE(REPLACE(TRIM(plate_number), ' ', ''), '-', ''));
CREATE UNIQUE INDEX IF NOT EXISTS idx_driver_unique_licence_number ON driver_profiles (UPPER(REPLACE(REPLACE(TRIM(licence_number), ' ', ''), '-', ''))) WHERE licence_number <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_driver_unique_plate_number ON driver_profiles (UPPER(REPLACE(REPLACE(TRIM(plate_number), ' ', ''), '-', ''))) WHERE plate_number <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_current_document ON driver_documents (driver_id, type) WHERE is_current = true;

INSERT INTO driver_documents (id, driver_id, type, file_path, file_url, status, is_current, created_at)
SELECT MD5('rida-legacy-licence:' || p.user_id::text)::uuid, p.user_id, 'driving_licence',
       'uploads/licences/' || REGEXP_REPLACE(REPLACE(p.licence_image_url, CHR(92), '/'), '^.*/', ''),
       '/api/v1/files/documents/' || MD5('rida-legacy-licence:' || p.user_id::text)::uuid::text,
       'submitted', true, COALESCE(p.created_at, CURRENT_TIMESTAMP)
FROM driver_profiles p
WHERE p.licence_image_url <> ''
  AND NOT EXISTS (SELECT 1 FROM driver_documents d WHERE d.driver_id = p.user_id AND d.type = 'driving_licence');

INSERT INTO verification_reviews (id, driver_id, document_id, decision, note, created_at)
SELECT MD5('rida-legacy-review:' || p.user_id::text)::uuid, p.user_id, d.id, 'legacy_import',
       'Existing licence preserved; full document submission and review required', CURRENT_TIMESTAMP
FROM driver_profiles p JOIN driver_documents d ON d.id = MD5('rida-legacy-licence:' || p.user_id::text)::uuid
WHERE NOT EXISTS (SELECT 1 FROM verification_reviews r WHERE r.id = MD5('rida-legacy-review:' || p.user_id::text)::uuid);

UPDATE users SET status = 'suspended' WHERE is_active = false AND status = 'active';

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM passenger_profiles p WHERE p.phone <> ''
      AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = p.user_id)
  ) THEN
    RAISE EXCEPTION 'Resolve orphan passenger phone metadata before migration; identities are redacted';
  END IF;
  IF EXISTS (
    WITH raw_phones AS (
      SELECT REPLACE(REPLACE(REPLACE(REPLACE(REGEXP_REPLACE(p.phone, '^[[:space:]]+|[[:space:]]+$', '', 'g'), ' ', ''), '-', ''), '(', ''), ')', '') AS phone
      FROM passenger_profiles p JOIN users u ON u.id = p.user_id
      WHERE p.phone <> '' AND COALESCE(u.phone, '') = ''
    ), canonical AS (
      SELECT CASE WHEN LENGTH(phone) = 10 AND LEFT(phone, 1) = '0' THEN '+255' || SUBSTRING(phone FROM 2)
                  WHEN LEFT(phone, 3) = '255' THEN '+' || phone ELSE phone END AS phone FROM raw_phones
    ) SELECT 1 FROM canonical WHERE phone <> '' AND phone !~ '^\+255[0-9]{9}$'
  ) THEN
    RAISE EXCEPTION 'Resolve invalid passenger phone metadata before migration; values are redacted';
  END IF;
END $$;
-- +goose StatementEnd

WITH raw_phones AS (
  SELECT p.user_id, REPLACE(REPLACE(REPLACE(REPLACE(REGEXP_REPLACE(p.phone, '^[[:space:]]+|[[:space:]]+$', '', 'g'), ' ', ''), '-', ''), '(', ''), ')', '') AS phone
  FROM passenger_profiles p JOIN users u ON u.id = p.user_id
  WHERE p.phone <> '' AND COALESCE(u.phone, '') = ''
), canonical AS (
  SELECT user_id, CASE WHEN LENGTH(phone) = 10 AND LEFT(phone, 1) = '0' THEN '+255' || SUBSTRING(phone FROM 2)
                      WHEN LEFT(phone, 3) = '255' THEN '+' || phone ELSE phone END AS phone FROM raw_phones
)
UPDATE users u SET phone = c.phone FROM canonical c WHERE u.id = c.user_id;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM users WHERE phone <> '' GROUP BY phone HAVING COUNT(*) > 1) THEN
    RAISE EXCEPTION 'Resolve duplicate account phones before migration; values are redacted';
  END IF;
END $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_phone_unique ON users (phone) WHERE phone <> '';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Backfill rollback is disabled; changes require a reviewed data plan'; END $$;
-- +goose StatementEnd
