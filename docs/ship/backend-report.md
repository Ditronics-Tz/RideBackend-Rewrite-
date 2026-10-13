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
