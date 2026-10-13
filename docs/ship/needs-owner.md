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
