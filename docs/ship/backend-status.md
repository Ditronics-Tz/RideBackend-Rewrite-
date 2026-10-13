# Backend implementation status

| Task | Status | Branch | Live endpoints / result |
| --- | --- | --- | --- |
| B1 | done | ship/be-secrets | Production config validation and redacted CI secret scan; owner credential work remains |
| B2 | done | ship/be-upload-quarantine | Startup migration removed; metadata inventory, private quarantine and restart journal; tests pass |
| B3 | done | ship/be-storage | Local/R2 adapters; private document presign/confirm; immutable evidence; count-only migration; provider setup pending |
| B4 | done | ship/be-runtime | Frozen goose migrations, readiness, request IDs, scoped CORS, worker cancellation, paging, Go 1.27.2 and early API inventory; local checks pass |
| B5 | done | ship/be-auth | App login/register/Google OAuth, rotating 15m/30d sessions, lockout/unlock, admin session/security events, immediate staff revocation and pending TOTP replacement; local checks pass, PostgreSQL concurrency checks await owner service |
| B6 | done | ship/be-b6-pii | Public profile DTOs, staff full-profile view, image normalization and per-user upload quota; local checks pass, PostgreSQL test service remains owner action |
| B7 | done | ship/be-b7-rides | Server-authoritative ride lifecycle, dispatch worker, participant reads, driver availability/location, generated contracts and route guards; local tests/vet/build/vulnerability scan pass; opt-in PostgreSQL concurrency test added, service not configured locally |
| B8 | done | ship/be-b8-support | Persisted support tickets, staff ticket workflow, support/admin ride operations and live counts, ride-bound ratings/admin moderation; local checks pass |
| B9 | done | ship/be-b8-support | Generated OpenAPI and route inventory checks; PostgreSQL service enables opt-in integration suites in CI; hosted CI run remains pending |
