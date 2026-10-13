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
detail resources retain their nested timelines; rides/tickets await B7/B8.
User counts and rating summaries use SQL aggregates.
Ride/ticket placeholders return 501; the former ticket update success without
saving is disabled. Stats explicitly mark unimplemented operational counts
unavailable, so the admin UI cannot display a fabricated zero as real data.

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

Changed file groups: config and environment example; CI/scanner and fixtures;
upload migration utilities/store/commands; storage adapters and tests;
document/profile models/controllers/routes; schema migrations and command;
runtime/request middleware/jobs/readiness; list query helpers and regression tests;
early API/guard inventory and generator; README and docs/ship handoff.
