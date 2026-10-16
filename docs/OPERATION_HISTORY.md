# Operation history for administrators

The API records metadata for registration, password and Google sign-in,
refresh, logout, profile changes, ride transitions, and other
POST/PUT/PATCH/DELETE API operations in `operation_events`. Existing
`app_security_events` and `staff_audit_logs` continue to hold security and staff
audit records. No mobile request or response contract changed.

Apply database migration 13 before starting the updated backend. An admin can
open **Settings -> Operations** or use
`GET /api/v1/admin/operation-events?limit=25&offset=0`. Optional exact-match
filters are `action`, `actor_type` (`app`, `staff`, `anonymous`), `actor_id`, and
`request_id`; `from` and `to` are inclusive `YYYY-MM-DD` dates in EAT. The
response has `events`, `total`, `limit`, and `offset`:

```json
{"events":[{"id":"00000000-0000-0000-0000-000000000001","actor_type":"app","actor_id":"00000000-0000-0000-0000-000000000002","action":"user_login","method":"POST","route":"/api/v1/auth/login","status":200,"request_id":"request-123","target_id":"","created_at":"2026-10-16T12:00:00Z"}],"total":1,"limit":25,"offset":0}
```

`status` shows whether an attempted operation succeeded. Failed sign-ins with
unknown accounts remain anonymous. Sensitive payloads, passwords, tokens,
query strings, and concrete URL paths are not stored. Frequent location pings,
ride quotes, and Google nonce creation are excluded to control database growth.
Rate-limited (429) requests are also excluded to prevent an abusive client from
filling the journal; bounded HTTP request logs still record their status.
This is an operational journal, not a tamper-evident financial ledger. If its
database insert fails, the API emits `operation_audit_write_failed` with the
request ID to the server log; it does not retry a completed user operation.
Existing accounts are not backfilled with invented historical events.
