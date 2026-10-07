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
