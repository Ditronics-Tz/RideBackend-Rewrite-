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

## 2026-10-13 - B4

- Server startup no longer changes database schemas. Run `go run ./cmd/migrate up`
  with injected DB configuration first. Startup refuses an unapplied schema;
  `status` only reports the recorded version. Existing compatible tables are
  adopted; incompatible column types/constraints require operator review.
- `GET /health` and `/api/v1/health` remain cheap. New `GET /ready` and
  `/api/v1/ready` ping the DB with a two-second timeout; return
  `{status,database,version,uptime_seconds}` or 503 on failure.
- Every HTTP response has X-Request-ID. JSON errors include `request_id`; safe
  incoming IDs (1-64 ASCII letters/digits/dot/underscore/dash) are preserved.
- CORS only allows explicit `CORS_ALLOWED_ORIGINS`; empty means no browser
  cross-origin access, wildcard is rejected. X-Request-ID and Retry-After are
  exposed. Same-origin/native clients remain compatible. Production DB_SSLMODE
  defaults to require; allow/prefer/disable are rejected, verify-full is supported.
- Existing paginated lists now default to limit=50, maximum=200, offset=0.
  Invalid bounds return 400 with `fields`. Users are always paginated and accept
  `role,status,q,verification_status`. No authoritative vehicle type exists yet.
  Status includes banned; inactive/deactivated are aliases for is_active=false
  (all restricted account statuses), preserving driver count card semantics.
- Audit lists retain `logs` and accept `q,action,actor` (actor_id alias), from/to
  (date or RFC3339), with total/limit/offset. Staff supports q/role/active|inactive.
- Verification queues support q/status and expiring_soon=true (within 30 days),
  with verifications/total/limit/offset. Notes, notifications, ratings and export
  lists also retain their collection keys and add pagination metadata. Available
  promos are paged after the existing eligibility evaluation, preserving its
  rules. User
  history pages both existing streams independently with the same limit/offset;
  `totals` provides each stream count and `total` is the maximum count.
- User stats now use COUNT queries. `operational_counts_available=false` marks
  the three legacy zero operational fields unavailable until B8 persists counts.
  Admin ride/ticket list and ticket update placeholders now return 501; ticket
  writes never claim an unsaved success. Existing nested
  resource timelines await B7/B8; this milestone does not make dispatch live.

Affected: admin tables and all clients fetching pageable collections; deployment
must apply schema migrations before starting the API and configure approved origins.

## 2026-10-13 - B5

- App registration requires a valid email and a non-common password of at least
  10 characters. App login returns the same `401 {error:"invalid credentials"}`
  for unknown accounts, wrong passwords, locked accounts and inactive accounts.
  Registration/login/OAuth are rate limited; five failed password attempts lock
  an account for 15 minutes. Admin can inspect sessions and security events,
  revoke sessions, and unlock accounts.
- App login and registration return `{token,access_token,refresh_token,
  expires_in,refresh_expires_at,restricted,user}`. `token` remains an alias for
  the 15-minute access token. Refresh tokens expire 30 days after the original
  session and are stored hashed. `POST /api/v1/auth/refresh` rotates both
  credentials; reuse revokes the token family. `POST /auth/logout`,
  `/auth/logout-all`, `GET /auth/sessions`, and `DELETE /auth/sessions/:id`
  revoke or list only the authenticated user's sessions. Refresh responses use
  Cache-Control: no-store.
- App suspensions, role changes, completed deletion, password changes and session
  revocation invalidate access sessions immediately. Pending deletion keeps its
  restricted recovery/export flow available. Staff access tokens check the
  database auth version and session on each request; credential/role/active
  changes revoke all staff sessions without process-local cache delay.
- Google login requires `GOOGLE_REDIRECT_ALLOWED_URLS` to include the exact
  configured redirect URI. State is one-use, short-lived, and bound to an
  HttpOnly browser cookie. Callback requires Google's verified email and binds
  by subject; it returns app tokens as JSON and never in a URL. Existing local
  accounts are not auto-linked by matching email.
- Staff authenticator setup requires the current password and, when replacing
  an active authenticator, a valid current code. A pending secret expires after
  ten minutes and becomes active only after its code verifies. Activation and
  disable revoke other staff sessions while returning a fresh current access
  token. Password change revokes all staff sessions and requires a new login.

Affected: passenger/driver apps, Google OAuth deployment configuration, and
admin/support account security screens. Apply migrations before server startup.
Frontend auth/settings and admin-session screens are implemented on
`ship/fe-auth-integration`; production provider and PostgreSQL concurrency checks
remain deployment work.
