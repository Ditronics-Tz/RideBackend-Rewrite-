# RideBackend

## Phase 6: block and report

App endpoints: `POST /blocks {user_id}`, `DELETE /blocks/:userId`,
`GET /blocks`, `POST /reports {reported_id,ride_id,category,description}` and
`GET /reports`. Lists use `limit` (default 20, max 100) and `offset`, with stable
date/UUID ordering. App report responses contain only the reporter's own report
ID, status and date. The reported account cannot look up a report or its author.
Blocks require an authoritative shared ride; matching must call
`Store.CheckRideEligibility(passengerID,driverID)`. Blocking does not alter a ride.
Reports are unique per reporter/reported/ride, capped at 10 in a rolling 24 hours.

| Staff endpoint (under `/api/v1`) | Support | Admin |
|---|---|---|
| GET `/admin/reports`, `/:id`, `/admin/users/:id/reports`, `/admin/flags` | Read | Read |
| POST `/admin/reports/:id/assign` | Claim self only | Assign active staff / override |
| POST `/admin/reports/:id/note` | Add internal note | Add internal note |
| POST `/admin/reports/:id/resolve` | Dismiss, warn, escalate, suspend with future `until` within 7 days | Also indefinite suspension, `banned`, explicit `override:true` |
| POST `/admin/flags/:id/resolve` | Resolve operational flag | Resolve |
| GET `/admin/users/:id/reports?include_blocks=true` | Denied | Paginated block list, audited |

Queue filters: `status`, `category`, `assignee`, `priority`, `user`, `before`
(RFC3339 for age). Resolution requires `outcome` and a nonempty `note`.
`/admin/complaints` GET and PATCH remain deprecated aliases: PATCH accepts the
same resolution body and never reports success without persisting a decision.
Three distinct reporters in 30 days create a flag and raise open-report priority;
configure `REPORT_FLAG_THRESHOLD` and `REPORT_FLAG_WINDOW_DAYS`. No auto-suspension.
Warnings and flag notifications are placed in a transactional delivery outbox.

Assumptions: phases 1â€“5 are not supplied. `store.ParsePage` supplies the missing
pagination helper. `models.Ride` is an internal authoritative integration record,
not a dispatcher: a future ride lifecycle must persist real participants, status,
fare and locations before these APIs can operate. No demo rides are inserted.
Account status, verification and privacy registries are reused. Existing app JWTs
are denied immediately during a suspension; this realm has no refresh sessions.
Reports/ride free text and precise locations are cleared on anonymisation;
internal report notes and reporter identity are never exported to a reported user.

**MOBILE APP CHANGES (phase 6):** add block/report actions to the trip and history
screens, submit the server ride UUID, and add paginated blocked-user settings.
Handle duplicate reports (409), ineligible pairs (403), and daily limit (429 with
`Retry-After`). Resolve a report only in the staff realm.

Go backend service built with Fiber v2, GORM, and PostgreSQL.

## Architecture & API Structure

All HTTP endpoints are strictly prefixed under `/api/v1`. Any route outside this prefix will return a JSON 404 response.

### Route Overview
- **System / Health**: `/api/v1/health`
- **Static Public Uploads**: `/api/v1/uploads/*` (profile pictures)
- **Protected File Downloads**: `/api/v1/files/licences/:id` (driver licence documents, accessible only by the driver or staff)
- **App Realm Auth**: `/api/v1/auth/*` (register, login, google, me)
- **Staff Realm Auth**: `/api/v1/staff/auth/*` (login, refresh, logout, logout-all, me, change-password, 2fa/*)
- **Admin & Management**: `/api/v1/admin/*` (staff management, audit logs, system stats, tickets, complaints, user oversight)

---

## Authentication

RideBackend uses two completely separated authentication realms with strict isolation. A token issued for one realm is rejected in the other realm.

### Realm Breakdown

| Dimension | App Realm (`aud="app"`) | Staff Realm (`aud="staff"`) |
| :--- | :--- | :--- |
| **Target Users** | Passengers & Drivers | Admin & Support personnel |
| **Data Storage** | `users` table | `staff_users`, `staff_sessions`, `staff_audit_logs` |
| **Signing Secret** | `JWT_SECRET` | `STAFF_JWT_SECRET` (>= 32 bytes, distinct from `JWT_SECRET`) |
| **Allowed Roles** | `passenger`, `driver` | `admin`, `support` |
| **Token Claims** | `iss="ride-backend"`, `aud="app"`, `sub=user_id`, `role` | `iss="ride-backend"`, `aud="staff"`, `sub=staff_id`, `role`, `jti` |
| **Middleware** | `middleware.AppProtected(cfg)` | `middleware.StaffProtected(cfg)` |
| **Self-Registration** | Allowed (passenger, driver only) | Disabled (Admin-provisioned only) |
| **Role Enforcement** | `RequireAppRoles(roles...)` | `RequireStaffRoles(roles...)` |

### Token Lifetimes & Security Policies

- **App Realm Access Token**: Default 72 hours (configurable via `JWT_TTL`).
- **Staff Realm Access Token**: Default 15 minutes (configurable via `STAFF_ACCESS_TTL`).
- **Staff Realm Refresh Token**: Default 7 days (configurable via `STAFF_REFRESH_TTL`).
- **Cryptographic Algorithms**: Only `HS256` is permitted (`alg=none` and asymmetric algorithms are rejected).
- **Staff Role Verification**: The staff token's `role` claim is informational. `StaffProtected` reloads the staff profile from the database (cached up to 30 seconds) to ensure real-time status and role accuracy.
- **Account Lockout**: After 5 failed staff login attempts, the account is automatically locked for 15 minutes.
- **Timing Attack Mitigation**: When an unknown email attempts to log in, bcrypt comparison is performed against a dummy hash to maintain constant-time response behavior.
- **Password Requirements**:
  - Staff passwords require a minimum of 12 characters and reject common dictionary passwords.
  - bcrypt cost factor is set to 12.
- **Refresh Token Rotation & Family Reuse Detection**:
  - Each refresh token can only be used once. A new session is generated upon rotation.
  - If a previously used or revoked refresh token is presented, the entire session family is revoked immediately to mitigate token theft.
- **Two-Factor Authentication (TOTP - RFC 6238)**:
  - Supports Google Authenticator / standard TOTP apps.
  - TOTP secrets are encrypted at rest using AES-256-GCM via `STAFF_TOTP_KEY`.
  - Configurable enforcement: `STAFF_REQUIRE_2FA_FOR_ADMIN=true` mandates 2FA setup before admins can execute admin-only operations.
- **First Login Password Change**:
  - Staff created with temporary passwords have `must_change_password=true`.
  - Until changed, all endpoints except `/me` and `/change-password` are blocked.

---

## Bootstrapping & Migration

### 1. Bootstrapping the First Admin (`cmd/create-admin`)

To initialize the first administrator in a fresh installation:

```bash
# Using environment variable for bootstrap password:
export STAFF_BOOTSTRAP_PASSWORD="YourStrongAdminPassword123!"
go run ./cmd/create-admin --name="Initial Admin" --email="admin@example.com"

# Or enter password interactively via stdin:
go run ./cmd/create-admin --name="Initial Admin" --email="admin@example.com"
```

> **Note**: `create-admin` will refuse to execute if any active administrator already exists in `staff_users`.

### 2. Migrating Legacy Staff (`cmd/migrate-staff`)

To migrate legacy staff members (`admin`, `support`, `mzee`) from the `users` table into the `staff_users` table:

```bash
# Perform a dry run (no database mutations):
go run ./cmd/migrate-staff --dry-run

# Execute the live migration:
go run ./cmd/migrate-staff
```

**Migration Behavior**:
- Idempotent: Skips users that already exist in `staff_users`.
- Role mapping: `admin` -> `admin`, `support` -> `support`, `mzee` -> `admin`.
- Sets `must_change_password=true` for migrated accounts.
- Disables the legacy record in the `users` table (`is_active=false`).
- Does not log or output credential hashes.

---

## Environment Variables

Refer to `.env.example` for all required and optional configurations:

```ini
# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=your_secure_db_password
DB_NAME=ride_backend

# Server & Network
PORT=8080
CORS_ALLOWED_ORIGINS=http://localhost:3000,http://localhost:5173

# App Realm Auth
JWT_SECRET=app_jwt_secret_here
JWT_TTL=72h

# Staff Realm Auth
STAFF_JWT_SECRET=staff_jwt_secret_minimum_32_characters_long_here
STAFF_ACCESS_TTL=15m
STAFF_REFRESH_TTL=168h
STAFF_TOTP_KEY=01234567890123456789012345678901
STAFF_REQUIRE_2FA_FOR_ADMIN=true
```

---

## Running Tests

Run the full automated test suite and static analysis:

```bash
# Run tests
go test -v ./...

# Run vet
go vet ./...
```

## Promo codes and referrals

All routes below are relative to `/api/v1`. Staff JWTs are required for
`/admin/*`; admins can write and support can only GET these promo/referral
resources. App routes require an active passenger and an app JWT.

Money and fares are `int64` whole Tanzanian shillings. Percent discounts use
integer percentages 1â€“100, round down, respect an optional `max_discount`, and
never exceed the fare. Codes are normalized to uppercase and must match
`[A-Z0-9-]{4,20}`. Dates are RFC3339, normalized to UTC; eligibility is inclusive
of `starts_at` and exclusive of `expires_at`.

| Method | Route | Purpose |
| --- | --- | --- |
| POST | `/admin/promos` | Create, optionally with `audience_user_ids` |
| GET | `/admin/promos?status=&audience=&q=&limit=20&offset=0` | Search and paginate |
| GET / PATCH | `/admin/promos/:id` | Detail / edit |
| POST | `/admin/promos/:id/pause`, `/resume`, `/archive` | Change stored status |
| GET / POST | `/admin/promos/:id/audience` | Search grants / add `user_ids`, `phones`, `emails` |
| DELETE | `/admin/promos/:id/audience/:userId` | Remove grant |
| POST | `/admin/promos/:id/share` | Notify current audience of an active promo |
| GET | `/admin/promos/:id/redemptions?limit=20&offset=0` | Redemption history |
| GET | `/admin/users/:id/promos` | Granted and redeemed promos |
| GET / PUT | `/admin/referral-settings` | Read / replace settings (PUT admin only) |
| GET | `/admin/referrals?status=&limit=20&offset=0` | Referral history |
| GET | `/promos/available?fare=5000` | Only eligible promos, with calculated discounts |
| POST | `/promos/validate` | Preview `{ "code": "WELCOME20", "fare": 5000 }` |
| GET | `/referral/me` | Stable invitation code, share text, and stats |
| POST | `/referral/apply` | Apply `{ "code": "ABCDEFGH" }` before the first ride |

Create example:

```json
{
  "code": "WELCOME20",
  "title": "Welcome aboard",
  "description": "20% off your first ride",
  "discount_type": "percent",
  "discount_value": 20,
  "max_discount": 3000,
  "min_fare": 2000,
  "audience": "all",
  "first_ride_only": true,
  "starts_at": "2026-10-01T00:00:00Z",
  "expires_at": "2026-12-31T23:59:59Z",
  "max_redemptions": 1000,
  "max_per_user": 1,
  "status": "active"
}
```

`max_discount` and `max_redemptions` are nullable. Creation defaults to draft
and one use per user. For selected promos, set `audience` to `selected` and add
passenger UUIDs through `audience_user_ids` or the audience endpoint. Initial
invalid recipients roll back creation; later additions return `added`,
`already_present`, `not_found`, and `not_passengers`. Repeated identifiers for
the same account are counted once. Audience pages support `q`, `limit`, `offset`.
An all-passenger promo remains eligible for future passengers too; sharing
creates grant rows only to track notifications to the current audience.

Stored statuses are draft, active, paused, archived. Responses add
`effective_status`: active promos become scheduled before their start and
expired at expiry. List filters accept those computed statuses. Archived promos
cannot resume. Once `redeemed_count > 0`, code, discount type/value and audience
type cannot change; title, description, expiry and limits remain editable.
`redeemed_count` counts reserved plus applied uses; `total_discount_given` sums
only applied discounts. Limits cannot be reduced below the reserved/applied total.

Validation errors return HTTP 400 with `{"error":"...","fields":{"field":"message"}}`;
duplicate promo codes return 409. Unknown codes and inaccessible selected codes
both return `invalid or unavailable code`; visibility is checked before exposing
expiry, scheduling, fare or limit details. Successful validation returns
`{valid, discount_amount, final_fare, promo:{code,title}}` and reserves nothing.
Validation is limited to 10 attempts per rolling minute per user (429 thereafter).
The limiter is per process; multiple API instances need shared rate-limit storage.
Pagination defaults to 20, maximum 100. `/admin/users` now supports `q` matching
name, email, phone or plate and optional `limit`/`offset`, while retaining the
legacy `count` and `users` fields and unpaginated behavior when paging is omitted.

Every admin mutation commits with its named `Store.CreateAuditLog` entry in the
same transaction. Audit metadata contains no recipient identifiers, phone numbers,
emails, codes, or request bodies. Existing general staff request auditing remains.

### Referral policy and notifications

Referrals default to disabled. PUT the complete settings object to enable them:

```json
{
  "enabled": true,
  "referee_reward_type": "fixed",
  "referee_reward_value": 1000,
  "referrer_reward_type": "fixed",
  "referrer_reward_value": 2000,
  "qualifying_min_fare": 3000,
  "max_rewards_per_referrer": 10,
  "reward_valid_days": 30
}
```

Reward types are fixed or percent. Settings in force at completion determine
rewards. A referee must be an active passenger, less than seven days old, have
a verified phone, and have no completed ride or previously applied referral.
Self-referral, inactive referrers and reused rewarded referee phone numbers are
rejected. A permanent `referral_reward_phones` ledger prevents phone reuse after
an account changes its number. The phone restriction applies to new referees;
referrers can invite multiple distinct passengers up to their configured limit.
Only the first completed ride can qualify: a first ride below the minimum rejects
the referral permanently. Disabled referrals or inactive accounts at completion
also reject it. Pending referrals are rechecked against the referrer reward cap
at completion, under a user lock.

Rewards are ordinary selected, single-use promos with `source=referral_reward`;
only the recipient receives a grant. System-generated rewards have no staff
creator. Passenger referrers receive a promo; driver referrers are marked
`pending_wallet` and receive no promo or wallet credit. There is no wallet yet.
The passenger referral API does not expose driver invitation codes, but the
store method supports driver referrers for future driver-app integration.

`Store.Notifier` is injectable. Its default `notifier.Logging` implementation
logs acceptance without personal data and sends no SMS or push. Sharing requires
an effective active promo and returns `notified_count`. `NotifiedAt` records
acceptance by the notifier, not delivery. Sharing again notifies again. A real
notifier must support retries/idempotency: external delivery cannot roll back if
the surrounding database transaction later fails; use an outbox for guaranteed
delivery when implementing that provider.

### Future rides integration (ride operations remain external)

The operations phases add an internal ride integration record for participants,
status, server fare, route context and location evidence. It supports these
operations modules but is not a booking or dispatch implementation. No ride
creation, matching, or driver-acceptance endpoints exist. A future rides service
must use server-authoritative passenger IDs, stable UUID ride IDs, and server
fares; it must never trust a client-supplied discount:

1. Call `EvaluatePromo(userID, code, fare, now)` for a preview. It returns
   `(discountAmount, *models.PromoCode, error)` without changing counters.
2. Call `ReservePromo(userID, code, fare, rideID)` before committing acceptance
   of the promo. It returns a `PromoRedemption`. Use its frozen `DiscountAmount`
   when charging. Reservation locks the passenger and promo, checks eligibility,
   then conditionally increments the counter only below the total cap. Both
   reserved and applied rows count against per-user limits.
3. On authoritative completion call `ConfirmPromo(rideID)` and
   `OnRideCompleted(rideID, passengerID, fare)`. The latter must run for **every**
   completed ride, even without a promo or referral. Pass the original fare
   before promo discount for the qualifying threshold. Complete these hooks and
   the future ride state transition atomically using a transaction-scoped store.
   `Store.Transaction(func(txStore *store.Store) error { ... })` composes the
   hooks with one another; future ride persistence must join the same DB transaction.
4. On cancellation before confirmation call `ReleasePromo(rideID)`. It marks the
   reservation released and decrements the total counter exactly once. Applied
   redemptions cannot be released; refunds need a separate future financial flow.

Retrying reserve with the same ride, user and code returns the original reservation;
fare changes do not recalculate it. A different code or user cannot reuse that
ride ID, and a released ride ID cannot be reserved again. Confirm/release are
idempotent for their own terminal state and harmless when no redemption exists.
The unique ride index and conditional SQL update enforce total capacity even
under concurrent requests. Callback completion receipts make reward issuance
idempotent; retry transient transaction errors. Business rejection of a referral
persists its reason and does not reject an otherwise valid completed ride.

`ride_completion_receipts` stores only ride UUID, passenger UUID and receipt time;
it is an integration ledger, with no fare, driver, route or ride lifecycle. First-ride
checks use receipts for completed callback IDs; receipts and their matching
ride rows must be written in one transaction by the future rides module. When
integrating an existing rides system, backfill every historical completion before
enabling first-ride promos/referrals. The future rides service must also serialize
each passenger's active ride lifecycle so two outstanding rides cannot both use
a first-ride-only promo before either completion is recorded.

Tests cover eligibility branches, overflow-safe arithmetic, audience secrecy,
reservation lifecycle, permissions, rate limits, audit entries and referral abuse.
SQLite exercises the SQL capacity guard sequentially. The real concurrent test
uses an isolated, randomly named PostgreSQL schema, drops only that schema, and
verifies exactly five successes out of thirty simultaneous reservations:

```powershell
$env:RUN_POSTGRES_PROMO_TESTS="1"
$env:POSTGRES_TEST_DSN="postgres://user:password@localhost:5432/ride_test?sslmode=disable"
go test ./store -run TestPostgresConcurrentPromoReservations -count=1 -v
```

The opt-in promo and payout concurrency tests use `POSTGRES_TEST_DSN`; they do not read or modify `.env`. Each creates and drops only its randomly named test schema. Run payout concurrency with `RUN_POSTGRES_FINANCE_TESTS=1` and `go test ./store -run TestPostgresConcurrentPayoutRequestsNoOverspend -count=1 -v`.

## Data protection

Passengers and drivers can request account deletion and a downloadable copy of
their data. Deletion uses a configurable grace period (14 days by default),
then anonymises the account in a transaction. The user UUID and retained
financial/operational references remain stable. Already-issued app tokens are
checked against current account status on every request: a pending account can
only read its own profile/request state, cancel deletion, or request/download an
export; deleted accounts cannot authenticate.

| Data | Deletion treatment | Retention reason |
| --- | --- | --- |
| User name, email, phone, password hash | Cleared; email replaced with `deleted+<id>@deleted.invalid`; password removed | Keep account UUID and role to preserve references; release the original email and phone |
| Passenger/driver profiles and uploaded images/documents | Names, phones, licence/card/plate values and image/file references cleared; owned files removed from disk | Keep profile/document UUIDs and verification decisions for referential integrity |
| Ratings | Keep scores and dates; clear comments written by the account and comments written about it | Preserve rating aggregates without retaining free text |
| Promos and referrals | Remove audience membership and referral code; clear referral free text and raw phone-reward ledger entries; retain redemption/referral UUID references | Keep discount and referral accounting linked to stable UUIDs |
| Deletion/export request records | Keep request IDs, statuses and timestamps; clear free text, errors and file paths | Compliance evidence and idempotent cleanup |
| Staff notes | Removed for the deleted user; never included in exports | Internal notes are not part of a user data package |
| Staff audit logs | Never deleted; new data-protection audit metadata contains IDs only | Security and accountability history |
| Rides, reports and safety | Stable ride/report/alert IDs remain; precise route/location and free text are cleared, user blocks and private contacts are removed. | Preserve operational counts and referenced IDs while removing identifying details |
| Payments and wallets | Immutable ledger entries, anonymised transaction IDs and required balances remain; encrypted destinations, raw identifiers and free text are cleared. | Financial retention period is a legal decision; confirm before production |
| Fraud review | Device hashes/location samples and internal notes are removed; anonymised rule outcomes/hold IDs remain. | Retain only investigation/accounting history that cannot directly identify the account |
| Staff users/sessions | Exempt from app-user export/anonymisation | Separate staff authentication realm; staff security records are not passenger/driver data |
| Referral settings | Exempt | Global configuration has no individual user data |

`FINANCIAL_RETENTION_YEARS` is a placeholder and must be confirmed with a
lawyer before production. No account-deletion tombstone is retained; therefore this implementation does not prevent a former banned user from signing up again after anonymisation. That trade-off avoids keeping a re-identification
hash without an approved retention policy.

The privacy registry exposes `DataExporter` modules (`Name`, `Export(userID)`
returning structured data and owned file paths) and `Anonymizer` modules
(`Name`, `Description`, `Anonymize(tx, userID)`). Modules self-register from
their own store files, so future rides/payments modules can register without
changing a central coordinator. The migration-coverage test checks every model
passed to `db.AutoMigrateModels()` against a registry entry or a written
exemption. Export archives contain `data.json` plus owned uploads under a
random filename in `EXPORT_DIR`, which must be outside `uploads/public`.
Download access is authenticated and owner-only. Exports expire after 168
hours by default. The notifier currently logs an acceptance without recipient
details; production SMS/push can be added through the notifier interface.

### Operator runbook

1. Apply normal database migrations by starting the API. Set
   `DELETION_GRACE_DAYS`, `EXPORT_TTL_HOURS`, `EXPORT_DIR`, and
   `RUN_BACKGROUND_JOBS` in the deployment environment (do not serve
   `EXPORT_DIR` as static content).
2. Run one idempotent sweep with `go run ./cmd/process-data-requests`. A cron
   or systemd timer may run it every few minutes. Alternatively set
   `RUN_BACKGROUND_JOBS=true` to enable the in-process five-minute ticker.
3. Support can inspect requests at `GET /api/v1/admin/data-requests?type=...`.
   Admins place legal holds with `POST /api/v1/admin/data-requests/:id/hold`
   and a reason. To resume, verify the hold has ended, then call
   `POST /api/v1/admin/data-requests/:id/release`; the worker rechecks blockers
   before anonymising. A blocked request remains blocked until an admin
   releases it after resolving its blocker.
4. Admin immediate execution requires a verification note confirming that the
   user asked to skip the grace period. Investigate failed requests from the
   stored generic error and server logs; do not add personal data to audit
   metadata or operational logs.

Deletion blockers are currently an extension registry. An internal ride record and wallet/payout tables now support active-ride and
financial-obligation checks. A real dispatcher may add stricter blockers. New
modules must register
both export and anonymisation behavior, document retained fields, and extend
blocker checks when those domains are added.

### Data-protection endpoints

App endpoints require a passenger/driver token. Request endpoints require the
current password, or for passwordless Google accounts a token issued within
the prior five minutes. A user may cancel only during the grace period. Export
requests are limited to one active request and one new request per 24 hours.

| Method | Route | Purpose |
| --- | --- | --- |
| POST / DELETE / GET | `/account/deletion-request` | Request deletion, cancel it, or view current status |
| POST | `/account/export-request` | Request a private data archive |
| GET | `/account/export-requests` | List own export requests |
| GET | `/account/export-requests/:id/download` | Download own ready, unexpired archive |
| GET | `/admin/data-requests?type=deletion\|export&status=&limit=&offset=` | Support/admin queue |
| GET | `/admin/data-requests/:id` | Request detail |
| POST | `/admin/users/:id/deletion-request` | Admin request on behalf of user, with required verification note |
| POST | `/admin/data-requests/:id/hold`, `/release`, `/execute` | Admin legal hold, release, or verified immediate execution |

Every user/admin request transition writes a staff audit event containing only
account/request IDs and fixed action names. Staff audit history is intentionally
retained through account anonymisation.


## Operations phases 6–9

Phases 6–9 use the existing Fiber routes, staff/app JWT realms, GORM models,
privacy registries, notification interfaces, admin Axios refresh flow, Radix
modals, tables, date formatting and Recharts chart. See [operations runbook](docs/OPERATIONS_PHASES_6_9.md)
for endpoint permission tables, provider assumptions, job commands, setup values,
retention notes, and the mobile app changes.

**Retention periods must be confirmed with a lawyer before production.**
`FINANCIAL_RETENTION_YEARS` is a deployment placeholder. Configure unique
secrets for `PAYMENT_ENCRYPTION_KEY` (32 bytes), webhook secrets and
`DEVICE_HASH_PEPPER`; never commit them. `PAYMENT_PROVIDER=mock` is for local
offline development and must not be used to accept real payments.

### Ride integration contract for phases 6–9

A rides module must persist authoritative ride participants, status, fare,
vehicle type, pickup/dropoff and expected distance. It may populate
`route_geometry` with a JSON polyline `[[lat,lng], ...]` from its trusted routing
service; clients cannot submit route geometry. Check
`Store.CheckRideEligibility(passengerID,driverID)` and
`Store.RequireDriverApproval(driverID)` before matching/accepting. Record only
assigned-driver location points through `POST /api/v1/rides/:rideId/location`.
Call `Store.OnRideCompleted(rideID,passengerID,fare)` once a real ride completes;
its receipt makes referral and rating/fraud events idempotent. For payment,
create the transaction only after validating the completed ride and its stored
fare; the amount in the client body is ignored for ride payments. Reserve promos
when a ride is accepted (before first-ride receipt), then call
`ConfirmPromo(rideID)` on settlement or `ReleasePromo(rideID)` on cancellation.
`POST /api/v1/rides/:rideId/sos` requires a real assigned/in-progress ride.
Never make these operations records a substitute for a booking system.

### Task review (2026-10-13)

The existing operations phases 6-9 were reused. The review corrected report
priority after severe incidents, notification batch delivery, discounted partial
refund rounding, device-platform capture, and shared-device false positives.
Postman token variables were repaired and are covered by go test ./postman.
Device capture accepts X-Device-Platform with X-Platform as a legacy fallback.
See docs/OPERATIONS_PHASES_6_9.md for permissions and mobile integration notes.

### Ship hardening: deployment environment

APP_ENV defaults to development. Set APP_ENV=production for deployment;
production validates independent app/staff JWT secrets of at least 32 bytes
and rejects example-like values. Secrets come from the deployment environment,
not from examples committed to this repository. See docs/ship/needs-owner.md
for owner credential-rotation actions. The push/PR CI runs a fully redacted
Gitleaks history scan; it intentionally has no exemption for tracked .env files.
The scanner fixture can be checked with python scripts/check-secret-scan.py.

### One-shot legacy upload migration

Startup no longer classifies or moves legacy uploads. Stop the API first, inject
configuration through the environment, and inventory with:

    go run ./cmd/migrate-uploads --dry-run

The command outputs counts only and does not migrate the database schema or
load .env. Run without --dry-run to move unknown files to private quarantine,
known documents to private storage, and only database-confirmed profile photos
to public storage. It also inspects files already under uploads/public. File
names do not determine visibility. Keep the private upload-migration journal
until the command succeeds; rerun it to recover interrupted metadata updates.
No file is overwritten. Quarantine review and deletion are operator decisions.
