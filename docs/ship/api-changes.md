# API changes

## 2026-10-13 - B1

Production startup now requires APP_ENV=production and independent JWT_SECRET /
STAFF_JWT_SECRET values of at least 32 bytes, rejecting example-like values.
Development remains the default. No endpoint or request payload changes.
Affected: deployment owner; admin/passenger/driver clients are unchanged.

## 2026-10-13 - B2

`/api/v1/uploads/*`: startup no longer publishes legacy non-licence files.
The one-shot `go run ./cmd/migrate-uploads --dry-run` reports counts only;
without --dry-run, database-confirmed photos can become public, known documents
remain private, and unknown files (including previously exposed public files)
move to private quarantine. Existing owner/staff document routes are retained;
migrated document URLs use the authenticated route. No mobile payload change.
Affected: deployment operator and any client holding an unsafe legacy public
document URL (refetch document metadata for its protected URL).

## 2026-10-13 - B3

- `STORAGE_DRIVER=local` keeps existing multipart uploads and authenticated binary
  document reads. Local presigning returns 501; callers use the legacy handler.
- R2: `POST /api/v1/driver/documents/presign` accepts
  `{type,content_type,size,expires_at}` (size 1-8388608 bytes, future expiry for
  licence/insurance); returns 201 `{document,upload:{url,method,headers,expires_at}}`.
  PUT the original bytes directly to this URL with the returned headers.
- `POST /api/v1/driver/documents/:id/confirm` validates ownership, five-minute
  upload deadline, size, declared and detected type; copies evidence to an
  immutable key and returns `{document}`. Retry after submission is safe.
  R2 legacy multipart document upload returns 410 with migration guidance.
- `GET /api/v1/files/documents/:id?presign=true` returns
  `{url,method,headers,expires_at,content_type,size}` for R2. Default R2 read is a
  redirect. Owner/staff guards and staff audit remain mandatory. Pending uploads
  are not readable. Fetch a new URL on open/expiry; never persist signed URLs or
  forward application tokens to storage.
- R2 profile uploads keep existing multipart request fields, persist object keys,
  and return `/api/v1/profile-images/:id` resolving to the configured public domain.
- One-shot `go run ./cmd/migrate-files-to-r2 --dry-run` inventories metadata;
  real migration requires configured R2 and verifies each copy before switching
  metadata. Existing local files are retained for operator reconciliation.
- New object-backed accounts have a deletion completion blocker until the
  operator supplies object cleanup/export integration (outside this run).

Affected: driver document uploader, passenger/driver profile photos, admin
document viewer and deployment operator. Mobile clients must implement the
presign -> direct PUT -> confirm flow when R2 is enabled.
