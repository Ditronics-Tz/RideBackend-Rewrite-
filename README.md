# RideBackend

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
integer percentages 1–100, round down, respect an optional `max_discount`, and
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

### Future rides integration (no rides module added)

The promo engine does not create ride records or ride endpoints. The future rides
service must use server-authoritative passenger IDs, stable UUID ride IDs and
fares, never trust a client-supplied discount:

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
checks currently rely on these receipts because no ride history exists. When
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
go test ./store -run TestPostgresConcurrentPromoReservations -count=1 -v
```

This opt-in test reads existing local DB configuration without modifying `.env`.
