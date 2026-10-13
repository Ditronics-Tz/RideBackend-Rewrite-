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
