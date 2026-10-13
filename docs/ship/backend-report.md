# Backend ship fix report

## B1 - Secret leak prevention

Implemented. Existing separate-realm/staff-key checks and empty credential
placeholders were reused. Added APP_ENV production validation, a Gitleaks rule
for application credential assignments, and push/PR scanning plus Go CI checks.
Validation: `go vet ./...` and `go test ./... -count=1` passed;
`python scripts/check-secret-scan.py` accepted the clean fixture, rejected the
planted-key branch and verified full value redaction. PostgreSQL opt-in checks
were unavailable locally. Hosted CI has not been run; historical tracked
credentials remain an owner dependency and are not suppressed in the scan.

Credential rotation and untracking the live `.env` are owner actions documented
in needs-owner.md. The secret scan does not suppress those findings.

## B2 - Upload migration

Implemented. Removed the filename-based automatic startup move.
Existing database document/profile metadata and owner/staff read routes are
reused. The one-shot command reads injected configuration, applies no schema
migrations, supports a mutation-free count-only dry run, gives private references
precedence, quarantines unknowns, rejects directory symlinks/path escapes, and
persists a private journal so metadata failures can be retried safely.

Targeted migration/privacy/restart/dry-run tests, `go vet ./...` and
`go test ./... -count=1` passed. PostgreSQL opt-in tests are unavailable locally.
The symlink test skips on Windows when creating a
symlink requires privileges unavailable to the process. Operator must stop the
API before the migration so static-file caches cannot serve old exposed files.

## B3 - Storage and document uploads

Implemented local and R2 adapters with confined keys, bucket separation, bounded
presign expiry, redacted provider errors and fake S3 tests. Existing local upload
and protected document reads remain available in development. R2 document PUTs
target temporary keys; confirmation verifies HEAD and actual bytes, copies to a
fresh immutable evidence key, and atomically submits the record. Reusing the PUT
cannot replace submitted evidence. Owner/staff authorization and staff read
audits are retained. Profile photos persist keys and resolve through the public
custom domain. No signed URLs are persisted.

Added a retryable one-shot migration for existing document/profile metadata. Its
dry run requires only injected database configuration, writes nothing, returns
counts and never deletes source files. Migration avoids unknown files and
external URLs. Existing incidents, exports and backups remain outside this run.
Accounts containing new object references cannot finish deletion until an
operator integrates storage cleanup/export; the explicit blocker preserves the
references instead of silently leaving untracked private objects.

Validation: storage fake-S3 tests, document lifecycle/ownership/audit tests,
migration retry/dry-run tests, `go vet ./...` and `go test ./... -count=1` passed.
Real Cloudflare, bucket CORS/lifecycle and PostgreSQL deployment checks require
owner configuration. Windows symlink coverage skips when privilege is absent.

## B4 - Runtime and database deployment

Implemented frozen goose SQL migrations for the 56-table existing schema, explicit
storage additions, and preserved verification/account backfills. Compatible
tables are adopted; conflicting types, required columns/constraints or duplicate
identity values fail with redacted errors. The API no longer AutoMigrates and
requires the current recorded schema version. Tests retain AutoMigrate solely
for isolated fixtures. Deployment uses a separate up/status command; rollback
requires a reviewed plan rather than a destructive command.

Added public health/readiness aliases, a two-second DB ping, production TLS
validation, explicit browser-origin allowlists, safe request IDs, JSON logs using
route patterns and no sensitive headers/bodies/query values, HTTP timeouts,
SIGTERM shutdown and cancellable worker database contexts. DSN credential values
are quoted and connection establishment is bounded. Existing list helpers now
use default 50/max 200; new users/staff/audit/verification SQL filters and
metadata are implemented. Banned/inactive status filters have regression tests.
Notes, ratings, notifications, history and export lists are bounded. Existing
detail resources retain their nested timelines; rides/tickets were not yet
implemented at the B4 checkpoint described here.
User counts and rating summaries use SQL aggregates.
At the B4 checkpoint, ride/ticket placeholders returned 501 and operational
counts were marked unavailable. B7 and B8 below replace those placeholders.

Updated to Go 1.27.2 after the installed 1.27.1 failed vulnerability checks.
`govulncheck ./...` now reports zero called/imported vulnerabilities. Its remaining
required-module advisory GO-2026-5932 concerns unimported x/crypto/openpgp, has no
fix, and does not justify removing the maintained bcrypt/TOTP dependencies.
Added the pinned vulnerability check to CI.

Early OpenAPI/route guard inventory describes existing routes and B3/B4 contracts;
future B5/B7/B8 endpoints are not invented. Coverage does not substitute for the
PostgreSQL CI and completed contracts required by B9.

Validation: full `go vet ./...`, `go test ./... -count=1`, `go build ./...` and
govulncheck pass locally; request correlation/redacted logs, ready with closed DB,
CORS denials, pagination/filter bounds and worker cancellation tests pass.
Frozen baseline application/adoption tests pass on SQLite. Actual PostgreSQL
adoption/advisory-lock tests remain opt-in and unavailable locally.

## Starting milestone and remaining backend work

B1-B4 are the starting implementation milestone. B5-B9 remain required: app auth
and sessions, public profile DTOs/image sanitization/IDOR fixes, authoritative
ride dispatch, persisted support/admin rides/rating moderation, and PostgreSQL CI.
Known public profile field exposure remains explicitly mapped for B6. The admin
UI contains controls for these contracts, but missing endpoints must stay honest
unavailable states. Credential rotation, storage lifecycle, host/browser checks
and production acceptance remain owner dependencies.

## B8 - Support, admin operations and ratings

Implemented persisted user-scoped support tickets, staff assignment/notes/status
updates, filtered admin ride search/detail/cancel/reassignment, live operational
counts, and ride-bound ratings. Each participant may rate once after completion;
admins can hide or restore ratings, and public aggregates/lists exclude hidden
ratings. The legacy user-to-user rating write can be disabled with
DISABLE_LEGACY_RATINGS. Support actions and ride/rating moderation write audit
records in the same transaction as their changes. Migration version 8 adds the
ticket/note tables and ride-rating fields/indexes.

## B9 - API contracts and CI

OpenAPI now describes the persisted B8 workflows and route guard inventory tests
cover the concrete routes. CI provisions PostgreSQL and enables migration,
authentication, finance, promo, verification, and ride integration suites, in
addition to vet, tests, vulnerability scanning, and build. Hosted CI has not yet
run; PostgreSQL integration remains unverified in this local environment.

Changed file groups: config and environment example; CI/scanner and fixtures;
upload migration utilities/store/commands; storage adapters and tests;
document/profile models/controllers/routes; schema migrations and command;
runtime/request middleware/jobs/readiness; list query helpers and regression tests;
early API/guard inventory and generator; README and docs/ship handoff.

## Files changed in this milestone

Compared with the preserved baseline branch; this list excludes the earlier task.txt corrections.

- `.env.example`
- `.gitattributes`
- `.github/workflows/ci.yml`
- `.gitignore`
- `.gitleaks.toml`
- `README.md`
- `apidoc/contract.go`
- `apidoc/contract_test.go`
- `apidoc/guards.go`
- `apidoc/inventory.go`
- `apidoc/schema.go`
- `cmd/generate-openapi/main.go`
- `cmd/migrate-files-to-r2/main.go`
- `cmd/migrate-files-to-r2/main_test.go`
- `cmd/migrate-uploads/main.go`
- `cmd/migrate/main.go`
- `cmd/migrate/main_test.go`
- `config/config.go`
- `config/cors.go`
- `config/production_test.go`
- `config/runtime_test.go`
- `config/storage_test.go`
- `controllers/account_controller.go`
- `controllers/admin_controller.go`
- `controllers/controllers_test.go`
- `controllers/data_request_controller.go`
- `controllers/document_storage_controller.go`
- `controllers/profile_controller.go`
- `controllers/promo_controller.go`
- `controllers/rating_controller.go`
- `controllers/verification_controller.go`
- `db/db.go`
- `docs/api/openapi.yaml`
- `docs/ship/api-changes.md`
- `docs/ship/backend-report.md`
- `docs/ship/backend-status.md`
- `docs/ship/mobile-changes.md`
- `docs/ship/needs-owner.md`
- `docs/ship/route-guards.md`
- `go.mod`
- `go.sum`
- `jobs/document_expiry.go`
- `jobs/operations.go`
- `main.go`
- `middleware/request.go`
- `middleware/request_test.go`
- `migrations/00001_baseline.sql`
- `migrations/00002_storage.sql`
- `migrations/00003_legacy_backfills.sql`
- `migrations/migrations.go`
- `migrations/migrations_test.go`
- `migrations/schema.json`
- `models/document_json.go`
- `models/user.go`
- `models/verification.go`
- `routes/admin_pagination_test.go`
- `routes/app.go`
- `routes/cors_test.go`
- `routes/document_storage_test.go`
- `routes/file_routes.go`
- `routes/profile_routes.go`
- `routes/readiness_test.go`
- `routes/routes_test.go`
- `scripts/check-secret-scan.py`
- `storage/local.go`
- `storage/r2.go`
- `storage/storage.go`
- `storage/storage_test.go`
- `store/admin_queries.go`
- `store/context_test.go`
- `store/document_upload.go`
- `store/object_storage.go`
- `store/operations_common.go`
- `store/pagination_test.go`
- `store/rating_queries.go`
- `store/storage_migration.go`
- `store/storage_migration_test.go`
- `store/storage_privacy_hook.go`
- `store/store.go`
- `store/upload_migration.go`
- `store/upload_migration_test.go`
- `utils/upload.go`
- `utils/upload_migration.go`
- `utils/upload_migration_test.go`

## B5 - Authentication and sessions

Implemented app registration/login validation, uniform invalid-credential responses,
SQL-backed rate limits, five-failure/15-minute account lockout, audited admin unlock,
and persistent app security events. App access tokens expire after 15 minutes and
include a session ID; refresh credentials are random 32-byte values stored as
SHA-256 hashes, rotate on use, and expire 30 days after original issuance. Reuse
revokes the complete family. Session list/revoke, logout/logout-all, support/admin
inspection, and admin unlock routes are available. Suspension, role change,
completed deletion, and explicit revocation invalidate app tokens immediately.

Google OAuth now uses exact configured redirect allowlisting, one-use stored state,
a browser-bound HttpOnly cookie, verified Google email, and subject-based identity
binding. Staff access checks database auth versions and session rows on every
request. Password and active/role changes revoke staff sessions. Authenticator
replacement stores a ten-minute pending secret, requires password and current
TOTP proof when replacing, activates only after a new valid code, revokes other
sessions, and returns a fresh current access token. Manual private file handlers
also enforce current session validity.

Validation: `go vet ./...`, `go test ./... -count=1`, and `go build ./...` pass.
Generated OpenAPI and route guard inventory are current. Store/migration unit tests
pass; opt-in PostgreSQL migration, locking, and refresh-concurrency tests were
skipped because no isolated PostgreSQL service is configured locally. Frontend
B5 auth/settings/admin-session integration and its checks are on
`ship/fe-auth-integration`.
## B6 - Public profile privacy and image uploads

Implemented explicit public driver/passenger profile DTOs. Public lookups exclude
phone, licence, vehicle-card, plate, verification assignment and document URLs;
owners retain full profile access. Support/admin full-profile lookup is mounted
behind staff session/role checks and existing admin route auditing. Driver
vehicle_type is now an explicit profile field, introduced by migration version 6.

Profile image upload now validates JPEG/PNG/WebP magic, decodes the full image,
limits dimensions/pixels, and re-encodes as PNG to strip source metadata. Both
local and R2 storage share the same validation. SQL-backed account quotas allow
three valid uploads per hour with Retry-After responses. Public profile schemas
and parameterized route guard inventory are regenerated.

Validation: profile privacy and upload-quota route tests, image normalization
unit tests, migration manifest checks, OpenAPI freshness/contract tests,
`go test ./... -count=1`, `go vet ./...`, and `go build ./...` pass.
`govulncheck ./...` found no vulnerabilities in reachable code. PostgreSQL
opt-in migration/concurrency checks require the owner test service.
