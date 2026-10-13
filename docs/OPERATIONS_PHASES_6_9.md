# Operations phases 6–9

All routes below are under `/api/v1`. App endpoints require passenger/driver JWTs; staff endpoints require staff JWTs. Pages use `limit` 1–100 (default 20), `offset` (default 0) and stable timestamp/UUID order. `429` responses set `Retry-After`. Support can handle ordinary operational queues and temporary account actions; finance and fraud confirmation are restricted as shown.

## Phase 6: blocks and reports

| Endpoint | Support | Admin |
| --- | --- | --- |
| App `POST/DELETE/GET /blocks` | — | — |
| App `POST/GET /reports` | — | — |
| `GET /admin/reports`, `GET /admin/reports/:id`, `GET /admin/flags`, `GET /admin/users/:id/reports` | Read | Read |
| `POST /admin/reports/:id/assign`, `/note`, `/resolve` | Claim self, note, dismiss/warn/escalate/suspend at most 7 days | All outcomes, override claims, permanent ban |
| `POST /admin/flags/:id/resolve` | Resolve | Resolve |
| Deprecated `/admin/complaints` aliases | Same report permissions | Same report permissions |

Blocks and reports require an authoritative shared ride. Reports are limited to 10 per user per rolling 24 hours and unique per reported person/ride. App report receipts never expose the reporter to the reported person. Three distinct reporters in the configurable window flag an account; flags do not suspend accounts automatically. Serious report actions require a note.

## Phase 7: safety

| Endpoint | Support | Admin |
| --- | --- | --- |
| App `/emergency-contacts`, `/rides/:id/sos`, `/sos/:id`, `/sos/:id/location`, `/sos/:id/safe`, `/rides/:id/share`, `/incidents` | — | — |
| `GET /admin/sos`, `/stream`, `/:id`, `/admin/incidents`, `/:id` | Read and acknowledge/assign, note, call attempt, escalate, resolve | Same; may override another claim |
| `GET /admin/sos/export` | — | CSV export |
| Incident assign/note/resolve | Operational actions | Operational actions |
| `GET /share/:token` | Public, rate-limited, uniform 404 | Public, rate-limited, uniform 404 |

Staff detail reads audit access to precise location/contact panels. The SOS stream sends only event type and opaque alert ID; clients fetch staff-authorized detail separately. It checks staff status every 15 seconds and ends at the staff access-token expiry. The dashboard reconnects and polls when SSE is unavailable. Share responses expose only driver first name, vehicle/plate, pickup/dropoff text, ride status and location age. SOS location pings are removed after `SOS_LOCATION_RETENTION_DAYS`; old resolved alert coordinates are cleared. False-alarm thresholds create flags but never punishment.

## Phase 8: payments and finance

| Endpoint | Support | Admin |
| --- | --- | --- |
| App `/wallet`, `/wallet/ledger`, `/wallet/topups`, `/rides/:id/pay`, `/payments`, `/driver/earnings`, `/driver/payouts` | — | — |
| `/admin/payments`, `/admin/payments/:id`, `/admin/refunds` | Read | Read |
| `POST /admin/payments/:id/refund`, `POST /admin/refunds/:id/approve` or `/reject` | Request and approve up to `REFUND_SUPPORT_LIMIT` | Any amount |
| `/admin/payouts`, payout actions/batch approve, `/admin/wallets/:id`, adjustments, payment settings, reconciliation, summary and CSV | — | Full access |

Every money-changing user request requires `Idempotency-Key`; payment and payout initiation is limited to five per user per minute. Ride fares come from the stored ride record; client-supplied amounts are ignored. Ride payments currently accept cash only, recorded against the completed ride and driver ledger. Wallet top-ups and provider-backed ride payments are unavailable until local providers are integrated. Cash commission can lower a driver wallet to the configured negative balance; online/matching eligibility blocks drivers below the limit. Wallet holds and immutable ledger entries are updated atomically. Admin CSV export excludes user phone/name and is audited.

`PaymentProvider` is the stable seam. There is no LipaGo API spec in this workspace, so `GatewayProvider` assumes bearer-authenticated JSON `POST /collections`, `/disbursements`, `/refunds`, `GET /transactions/{reference}`, and `GET /transactions?from=&to=` returning `payments.Result` fields. Each request carries `Idempotency-Key`; provider must persist and deduplicate it. Webhooks send `X-Payment-Timestamp` (Unix seconds), `X-Payment-Signature` (hex HMAC-SHA256 over `timestamp + "." + raw body`) and a JSON event. Current and previous secrets are accepted within five minutes. Confirm these paths, payloads, refund semantics and signature format with the actual gateway before enabling real transactions. Use an adapter if its spec differs. `PAYMENT_PROVIDER=mock` is local/offline only. `PAYMENT_ENCRYPTION_KEY` must contain exactly 32 bytes; encrypts transient phone destinations at rest. Never log raw body, phone or provider errors.

Run `go run ./cmd/process-payments` from the backend repo for initiation/polling, payout disbursement, provider refund retries and reconciliation work. Use `--reconcile` for an explicit reconciliation sweep. The main process calls the job every 10 seconds only when `RUN_BACKGROUND_JOBS=true`. Nightly wallet reconciliation and provider comparisons produce exceptions for finance staff. For multiple workers, database conditional claims and idempotency keys guard settlement; verify the gateway guarantees idempotent disbursement/refunds before adding workers.

## Phase 9: fraud review

| Endpoint | Support | Admin |
| --- | --- | --- |
| `GET /admin/fraud/flags`, `/:id`, `/admin/users/:id/fraud`, `/admin/fraud/stats`, `/admin/fraud/rules` | Read | Read |
| Flag assign/note; dismiss low-severity only | Yes | Yes |
| Confirm, high-severity decisions, payout hold/release, rule updates, dry-run | — | Yes |
| App driver `POST /rides/:id/location` | — | — |

The device ID header is `X-Device-ID`; supported `X-Device-Platform` values are `android`, `ios` and `web`. Only a SHA-256 hash with `DEVICE_HASH_PEPPER` is stored, with raw IP omitted from API responses and exports. The driver's authenticated in-progress location endpoint is rate-limited to 30 points per minute and retained for 30 days. High-severity rules default to payout and promo holds; no rule automatically bans an account. Dismissing a flag releases its holds and suppresses the same dedupe key for 30 days. Confirmation may suspend only through the account-status method and requires admin.

The location rule checks movement ratio, repeated points, impossible speed, and distance from an optional trusted route polyline. Existing dispatch/routing geometry was absent, so route geometry is an optional internal field populated only by a future trusted routing service; without it the rule still evaluates the other location checks. Do not treat a map line from the client as route evidence. Rule status reports disabled when its required event data or pepper is unavailable. Dry-run evaluates existing data and writes no flags/actions/holds. Configure numeric whole-number thresholds in `/admin/fraud/rules`; evidence stores IDs and counts only.

Run `go run ./cmd/process-fraud` for queued events; add `--nightly` to queue the daily account pass. The optional application ticker processes queued events every ten seconds. The location cleanup runs in that same idempotent job. User detail links expose only staff-visible linked accounts and are paginated.

## Configuration and privacy

Add the keys from `.env.example` to the deployment secret manager. Do not copy production credentials to `.env.example`. `RUN_BACKGROUND_JOBS=false` means use the `cmd` jobs under a scheduler. Run all database migrations before workers. Confirm secret rotation and job monitoring before exposing mobile-money calls. Staff audit events store action, opaque subject ID, staff ID, timestamp and numeric/status-only metadata; never put phone numbers, names, descriptions, tokens, device IDs/hashes or file data in audit metadata. Staff audit logs are exempt from deletion and must be retained; keep their metadata free of personal data.

| Data | Removed/anonymised | Retained |
| --- | --- | --- |
| User/profile/driver documents | Name, email, phone, credentials, licence/card/plate and uploaded files are cleared/removed | User UUID, role, stable profile/document references and verification decision |
| Ratings and reports | Comments, report text, staff notes and user blocks are cleared/removed | Scores, statuses, dates and anonymous stable IDs |
| Emergency contacts/SOS/incidents | Contacts, share tokens, private files, detailed locations and free text are removed/cleared | Anonymised alert/incident IDs and statuses |
| Payments/payouts | Phone ciphertext/masks/hashes and personal notes are cleared; user links are anonymised | Immutable ledger, necessary amounts and stable transaction IDs |
| Fraud | Device and location samples, linked identifiers and investigation notes are removed | Rule configuration and anonymous hold/action history |
| Staff audit log | Never deleted by app-user deletion | Security/accountability trail; metadata is IDs and non-PII only |

Financial retention is provisional. **A lawyer must confirm retention periods** before production. No deletion tombstone is currently retained, so a previously banned user may re-register after anonymisation. This is an explicit privacy choice pending an approved lawful basis for retaining a salted identifier hash.

## Operator runbook

1. Set gateway and encryption secrets in the deployment secret manager; use mock only for development. Keep exports and incident uploads on private storage.
2. Apply migrations by starting the API. If jobs run out-of-process, schedule `go run ./cmd/process-safety`, `go run ./cmd/process-payments`, `go run ./cmd/process-fraud` and `go run ./cmd/process-data-requests` at their documented cadence. The application ticker runs safety/payment/fraud every ten seconds when enabled; data-request processing uses its own five-minute ticker.
3. For a safety alert, acknowledge it to claim it, call the parties/contacts using the audited reveal flow, record each call/note, and resolve only with an outcome and note. Escalations are retried safely; check outbox delivery status when a provider fails.
4. For a reconciliation exception, compare provider reference/amount/status with the immutable ledger before resolving. Never repair balances by editing a ledger row; use an admin wallet adjustment with a reason and audit entry.
5. For a fraud flag, review only staff-visible evidence, add a note, and resolve by role/severity. Admins release holds only after checking the provider and wallet state. A dismiss releases automatic holds and suppresses matching detections for 30 days.
6. For a legal deletion hold, an admin records the reason, then releases it when counsel/investigation confirms. Re-run the worker after release; blockers are checked again before anonymisation.

## Mobile app changes

- **Phase 6:** add block/report actions on trip and history screens, send the ride UUID, handle 409 duplicates and 429 `Retry-After`, and add a blocked-users settings screen.
- **Phase 7:** add hold-to-confirm and silent SOS, send authenticated location pings while SOS is open, manage emergency contacts, share-trip links, and submit incident reports with private attachments.
- **Phase 8:** show cash as the current ride payment method, retain ledger and earnings views, and handle cash-limit errors (`CASH_LIMIT_REACHED`). Add provider-backed methods and wallet top-ups only after local-provider contracts and integration are ready. Never report a payment as successful before server confirmation.
- **Phase 9:** send a stable installation `X-Device-ID` on every authenticated request and `X-Device-Platform`; driver apps send location points during assigned/in-progress rides. Do not send a client-calculated route or use fraud findings to block a user locally.

## Reused modules and assumptions

Account deletion/export, promo/referral engines, driver verification and rating aggregation already existed in this checkout and were extended through their registries/hooks. No ride booking, dispatch or existing payment gateway API spec was present, so a limited internal ride record and the documented gateway adapter assumption were added. The operations admin UI uses the existing React/Vite/TanStack/Axios/Zustand/Radix/Sonner/Recharts stack. It has no map dependency, so the SOS and fraud evidence maps use attributed OpenStreetMap raster tiles in a small native SVG view. Provider SMS/push adapters remain mock/logging implementations behind the notification interface until credentials and delivery-provider contract are supplied.

## Task review corrections (2026-10-13)

The models, routes, privacy registries, provider interfaces, background commands,
staff permissions and UI pages for phases 6-9 already existed and were reused.
The following changes repair defects without changing endpoint shapes:

- Phase 6: flagged-account links now forward the user filter in the reports UI.
- Phase 7: a severe incident raises an existing open/in-review report to high
  priority without overwriting its evidence. Notification batches attempt all
  selected recipients, preserve failed deliveries for retry, and return delivery
  errors after the batch. The SOS UI has pagination past 100 alerts and is the
  support staff default home; login retains requested query parameters.
- Phase 8: partial refunds allocate commission from the rounded gross fare
  being reversed. All ledger amounts stay positive, each refund balances, and
  full refunds reverse the full original commission and subsidy.
- Phase 9: login and authenticated requests accept X-Device-Platform, including
  CORS preflight; X-Platform remains a fallback. Shared-device thresholds apply
  to each device separately, with linked accounts limited to offending devices.
- All phases: Postman authorization variables now have matching double braces.
  Controller tests use an explicit ten-second request timeout and report errors
  before inspecting the response; production password costs are unchanged.

Backend implementation files changed: controllers/auth_controller.go,
middleware/auth_middleware.go, routes/app.go, store/safety_jobs.go,
store/payout_store.go and store/fraud_rules.go. Regression coverage was added in
routes/device_capture_test.go, routes/cors_test.go, store/fraud_test.go,
store/operations_regression_test.go and postman/collection_test.go, with request
handling repaired in controllers/controllers_test.go. Postman examples and the
README were updated. The existing driver verification, account status,
notifications, payment provider, ledger, promo and privacy implementations were
retained.

Run go vet ./... and go test ./... from the backend folder. The PostgreSQL
concurrency/integration checks require explicit test flags and an isolated
POSTGRES_TEST_DSN; they are skipped when those are not configured. Production
ride dispatch, real SMS/push delivery and a confirmed gateway contract remain
existing integration dependencies, as documented above.

MOBILE APP CHANGES: the existing phase 6-9 adoption lists above still apply.
The header contract is additive: prefer X-Device-Platform; existing X-Platform
clients remain supported. No new breaking endpoint or database migration is
required by these corrections.
