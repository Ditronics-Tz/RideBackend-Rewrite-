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
