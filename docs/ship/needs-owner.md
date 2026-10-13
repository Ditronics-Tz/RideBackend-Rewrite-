# Owner actions

The implementation instructions reserve these actions for the owner. Agents
continue implementation using mocks and interfaces.

- Rotate the exposed credential classes identified by the combined report:
  Google OAuth, database, app/staff JWTs and staff TOTP encryption. Invalidate
  previous sessions/credentials and confirm old credentials are unusable.
- After rotation, untrack the live `.env` without publishing its contents or
  rewriting history. No agent has opened or modified that file. The new secret
  scan intentionally does not ignore known exposure; CI may fail until the owner
  has handled the tracked file/history according to the approved policy.
- Supply approved production OAuth redirect allowlist, storage lifecycle rules,
  private/public R2 buckets and credentials via deployment secrets.
- Confirm legal evidence/retention and production service/provider choices.
- Supply an isolated PostgreSQL test service for concurrency checks and host
  CI execution. Local checks do not establish deployed launch readiness.
- Configure distinct public/private R2 buckets and a public HTTPS custom domain.
  Never provide a backup bucket to the application. Private buckets must have no
  public endpoint. Set browser PUT CORS to approved client origins and headers,
  including Content-Type; do not expose application Authorization to storage.
- Set an expiry lifecycle on private `staging/documents/` objects after an agreed
  short recovery window. Do not apply temporary-object expiry to `documents/`
  evidence. Configure incomplete multipart cleanup and retention based on legal
  requirements. Reconcile unused immutable copies after failed DB commits and
  retained local source files after successful migration.
- Integrate private object cleanup and export before enabling completed account
  deletion for object-backed users. An explicit blocker currently retains keys;
  incident/export storage adapters and backups are outside this implementation.
