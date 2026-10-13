# Backend implementation status

| Task | Status | Branch | Live endpoints / result |
| --- | --- | --- | --- |
| B1 | done | ship/be-secrets | Production config validation and redacted CI secret scan; owner credential work remains |
| B2 | done | ship/be-upload-quarantine | Startup migration removed; metadata inventory, private quarantine and restart journal; tests pass |
| B3 | done | ship/be-storage | Local/R2 adapters; private document presign/confirm; immutable evidence; count-only migration; provider setup pending |
| B4 | done | ship/be-runtime | Frozen goose migrations, readiness, request IDs, scoped CORS, worker cancellation, paging, Go 1.27.2 and early API inventory; local checks pass |
| B5 | done | ship/be-auth | App login/register/Google OAuth, rotating 15m/30d sessions, lockout/unlock, admin session/security events, immediate staff revocation and pending TOTP replacement; local checks pass, PostgreSQL concurrency checks await owner service |
| B6 | not started | - | Public profile DTOs and upload safeguards |
| B7 | not started | - | Authoritative ride lifecycle |
| B8 | not started | - | Support, admin rides and ratings |
| B9 | not started | - | Early route inventory/coverage provided under B4; PostgreSQL CI and completed endpoint contracts remain |
