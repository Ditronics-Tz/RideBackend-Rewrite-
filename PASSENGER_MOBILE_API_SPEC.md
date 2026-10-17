# Rida Passenger Android API Integration Specification

**Audience:** Android passenger app developers (Kotlin, Jetpack Compose, Retrofit, OkHttp, Moshi)  
**API host:** `https://rida-backend.ditronics.co.tz`  
**API version prefix:** `/api/v1`  
**Contract status:** Written from the current Go routes and handlers in this repository. The generated machine-readable contract is [`docs/api/openapi.yaml`](docs/api/openapi.yaml). This document covers the passenger app surface; driver and staff-only routes are excluded.

> Use `POST /api/v1/auth/login` for app login. `GET /login` is not a web page and is expected to return 404.

## 1. Base URL and transport

Production base URL: `https://rida-backend.ditronics.co.tz/api/v1`. All production API traffic must use HTTPS. Send JSON requests with `Content-Type: application/json` and `Accept: application/json`. Multipart endpoints use the platform-generated multipart boundary. Times are RFC 3339 UTC timestamps. IDs are UUID strings unless stated otherwise. Coordinates are decimal degrees: latitude `[-90, 90]`, longitude `[-180, 180]`.

Useful unauthenticated checks:

| Method | Path | Response |
| --- | --- | --- |
| GET | `/health` | `200 {"status":"ok","version":"1.0.0"}` |
| GET | `/ready` | `200` when database is reachable; `503` otherwise |

The same health paths are also available below `/api/v1`.

## 2. Headers

Send these headers on normal JSON requests:

```http
Accept: application/json
Content-Type: application/json
Authorization: Bearer <accessToken>
X-Device-ID: <stable-random-installation-id>
X-Device-Platform: android
X-Request-ID: <optional-client-generated-correlation-id>
```

`Authorization` is required on protected endpoints. `X-Device-ID` and `X-Device-Platform` are informational metadata for session management, not authentication proof. Do not use Android ID, advertising ID, or other cross-app hardware identifiers as the device ID; generate and persist a random app installation ID. Every response has an `X-Request-ID`; JSON error responses also include `request_id`.

For state-changing operations that require idempotency, also send:

```http
Idempotency-Key: <unique-random-key-per-user-action>
```

Use the same key when retrying the same action after a timeout. Generate a new key for a distinct action. Keys must be 8–100 characters.

## 3. Authentication and persistent sessions

### 3.1 Registration

`POST /auth/register` is public. Registration creates a passenger by default. `role` is optional; the passenger app should omit it or send `passenger`.

Request:

```json
{
  "name": "Asha Mushi",
  "email": "asha@example.com",
  "password": "a-long-unique-password"
}
```

Constraints: name 1–100 characters; valid email; password at least 10 characters, at most 72 bytes, and not a common password. Email is normalized to lowercase. Successful registration returns HTTP 201 and the session response described in §3.4. Duplicate/invalid registrations return a 4xx response; do not depend on the error text for program logic.

### 3.2 Email or phone and password login

`POST /auth/login` is public. Exactly one of `identifier`, `email`, or `phone` must be provided along with `password`. Prefer `identifier`; it accepts an email or phone number. Phone numbers are canonicalized server-side; the expected mobile format is a Tanzania number such as `+255712345678` or `0712 345 678`. Phone login requires verified phone ownership.

Request by email:

```json
{"identifier":"asha@example.com","password":"a-long-unique-password"}
```

Request by phone:

```json
{"identifier":"0712 345 678","password":"a-long-unique-password"}
```

Legacy field forms supported by the server:

```json
{"email":"asha@example.com","password":"a-long-unique-password"}
```

```json
{"phone":"+255712345678","password":"a-long-unique-password"}
```

Unknown account, incorrect password, locked account, and inactive account use a generic `401` invalid-credentials response. Repeated failed attempts can lock password login for 15 minutes. Authentication endpoints are rate limited; honor `429` and `Retry-After` when present.

### 3.3 Google sign-in

Use Google Identity Services on Android to obtain a Google **ID token** with the backend configured Web client ID as the audience. Never ship the Google client secret in Android. The backend verifies the token and creates/loads a Rida session; the Google token is not the Rida API bearer token.

#### Step A — obtain a one-use challenge

`POST /auth/google/challenge` (public), empty JSON body `{}`.

```json
{
  "challengeId": "challenge-uuid-or-opaque-id",
  "nonce": "server-generated-cryptographic-random-value",
  "expiresAt": "2026-10-14T12:00:00Z"
}
```

Use the returned nonce in the Android Google sign-in request so the resulting ID token contains that nonce. The challenge is short-lived and single-use.

#### Step B — exchange the ID token

`POST /auth/google` (public):

```json
{
  "idToken": "<google-id-token>",
  "challengeId": "<challengeId-from-step-A>"
}
```

Success returns HTTP 200 and the same Rida session response as password login. Invalid Google token: `401`, code `GOOGLE_TOKEN_INVALID`. Missing, expired, mismatched, or already-consumed challenge: `401`, code `GOOGLE_CHALLENGE_INVALID`. A verified Google identity whose email belongs to an existing unlinked password account returns `409`, code `ACCOUNT_LINK_REQUIRED`; the backend does not silently link accounts by email. If Google is not configured server-side, the endpoint returns `503`, code `GOOGLE_NOT_CONFIGURED`.

If an app cannot pass nonce through its selected Google SDK flow, do not skip the challenge or send a fabricated nonce; use a Google sign-in implementation that supports nonce-bound ID tokens.

### 3.4 Session response

Registration, password login, Google exchange and refresh return these app session fields (the server currently also includes `message`, legacy aliases and user status metadata):

```json
{
  "user": {
    "id": "7db87b10-87ef-4e95-9c93-02e37a6f9aa1",
    "name": "Asha Mushi",
    "email": "asha@example.com",
    "phone": null,
    "role": "passenger",
    "status": "active",
    "is_active": true,
    "phone_verified_at": null,
    "suspended_until": null
  },
  "accessToken": "<rida-access-token>",
  "expiresIn": 900,
  "refreshToken": "<opaque-refresh-token>",
  "sessionId": "7db87b10-87ef-4e95-9c93-02e37a6f9aa1",
  "onboardingRequired": ["phone_verification"],
  "restricted": false,
  "token": "<same-rida-access-token>",
  "access_token": "<same-rida-access-token>",
  "refresh_token": "<same-opaque-refresh-token>",
  "expires_in": 900,
  "refresh_expires_at": "2027-01-12T12:00:00Z",
  "message": "Login successful"
}
```

Types: identifiers and tokens are strings; `phone` is a string or JSON `null`; `phone_verified_at` and `suspended_until` are timestamp strings or `null`; `expiresIn` / `expires_in` are integer seconds; `onboardingRequired` is an array of strings. The current onboarding marker is `phone_verification`. It is required if a passenger has no phone or an unverified phone. Do not allow booking until the app has completed phone verification and refreshed `/me`.

The camelCase fields are the mobile contract. The snake_case aliases and `token` are compatibility fields; new client code should use `accessToken`, `expiresIn` and `refreshToken`. `refresh_expires_at` currently reports the session inactivity expiry (which may move forward on refresh); server-side absolute session lifetime is enforced separately.

### 3.5 Refresh and secure storage

`POST /auth/refresh` is public to the session credential (no bearer token required):

```json
{"refreshToken":"<current-refresh-token>"}
```

Every successful refresh rotates the refresh token and access token. **Atomically replace both stored credentials** with the response; never retry refresh with the old token after receiving success. Refresh tokens are opaque, stored hashed by the backend, and bound to a per-device session. Current configured policy defaults are 15-minute access expiry, 90-day inactivity timeout and 365-day absolute lifetime. Activity can extend inactivity expiry but cannot extend the absolute deadline. Reuse of a rotated token revokes its token family.

Use Android Keystore-backed encrypted storage (for example, an encrypted DataStore implementation) for the refresh token. Keep access tokens in memory where practical; never put either token in logs, analytics, crash reports, clipboard, URLs, or plaintext preferences. On app start, try refresh if there is no valid access token. On a protected API `401`, refresh once and retry the original request at most once. Use a single-flight mutex so concurrent requests do not refresh the same token in parallel. Do not clear credentials for timeouts, network errors, or `503`.

### 3.6 Current user, logout, and session list

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| GET | `/me` | Bearer | Mobile current user and onboarding status |
| GET | `/auth/me` | Bearer | Expanded account/profile response (compatibility endpoint) |
| POST | `/auth/logout` | Bearer | Revoke this app session |
| POST | `/auth/logout-all` | Bearer | Revoke every app session for this user |
| GET | `/auth/sessions?limit=50&offset=0` | Bearer | List this user's sessions |
| DELETE | `/auth/sessions/{sessionId}` | Bearer | Revoke one session belonging to the user |

`GET /me` response:

```json
{
  "user": {
    "id": "7db87b10-87ef-4e95-9c93-02e37a6f9aa1",
    "name": "Asha Mushi",
    "email": "asha@example.com",
    "phone": null,
    "role": "passenger",
    "status": "active",
    "is_active": true,
    "phone_verified_at": null,
    "suspended_until": null
  },
  "onboardingRequired": ["phone_verification"]
}
```

Logout success is `200 {"message":"Logged out"}`; logout-all is `200 {"message":"All sessions revoked"}`; delete-session success is `204` with no body. Revocation invalidates protected access immediately because the API checks the session on requests. Clear local credentials after successful logout; if the network is unavailable, clear the local account state and explain the server session may remain until it expires.

### 3.7 Phone verification

Requires bearer token. Actual SMS delivery depends on a configured backend SMS provider.

| Method | Path | Body | Success |
| --- | --- | --- | --- |
| POST | `/auth/phone/request-otp` | `{"phone":"+255712345678"}` | `{"message":"Verification code sent","expires_in":300}` |
| POST | `/auth/phone/verify-otp` | `{"code":"123456"}` | Current-user response |

OTP is short-lived, limited and must be treated as sensitive. Do not log it. After verification, call `GET /me` to confirm the onboarding marker is cleared.

## 4. Passenger profile

All endpoints require bearer authentication and the passenger role where stated.

| Method | Path | Request / response |
| --- | --- | --- |
| GET | `/passenger/profile/me` | Own passenger profile |
| PUT | `/passenger/profile/me` | Update `full_name`; phone changes must use OTP routes |
| POST | `/passenger/profile/me/image` | Multipart field `profile_picture`; JPEG/PNG/WebP, max 5 MiB source |
| GET | `/passenger/profile/{userId}` | Safe public passenger profile only |
| GET | `/passenger/preferences` | Passenger preferences |
| PATCH | `/passenger/preferences` | Partial preference update; explicit `false` is supported |
| GET | `/passenger/saved-places` | `{ "places": [...] }` |
| POST | `/passenger/saved-places` | Create a saved place; returns 201 place |
| PUT | `/passenger/saved-places/{id}` | Replace/update a saved place |
| DELETE | `/passenger/saved-places/{id}` | 204 on success |

### Profile photo for Android

Send `POST /api/v1/passenger/profile/me/image` with the passenger bearer token and one `multipart/form-data` file part named `profile_picture`. The source must be a valid JPEG, PNG or WebP image of 1–5,242,880 bytes, with width and height between 1 and 4,096 pixels and no more than 16,777,216 pixels total. The backend decodes and stores a metadata-free PNG, using a new generated filename on every replacement. Do not set the multipart boundary manually in OkHttp/Retrofit.

Successful upload (`200`) includes `{"profile_picture_url":"https://rida-backend.ditronics.co.tz/api/v1/uploads/passenger_...png"}` in the existing profile object. `GET /api/v1/passenger/profile/me` returns the same field; it is `null` if no image exists. The URL is public and can be loaded without an Authorization header. R2 storage uses its configured HTTPS public media domain instead of `/uploads`. Each replacement returns a different URL, so clients can use the new URL as the cache key. A failed upload leaves the previous photo unchanged.

Upload errors use JSON `{"error":"...","fields":{"profile_picture":"..."}}` for image validation: `401` missing/invalid app session, `403` wrong role, `413` source or normalized image over 5 MiB, `415` non-multipart request or unsupported JPEG/PNG/WebP format, `422` missing/empty/corrupt image or invalid dimensions, `429` upload quota (three validated upload attempts per passenger per hour; `Retry-After` header), and `503` unavailable storage/rate limiter. For a retryable `503`, keep showing the existing avatar. The image URL must be an HTTPS public origin in production (`PUBLIC_BASE_URL` for local storage or `R2_PUBLIC_BASE_URL` for R2). No Android request change is needed beyond consuming the absolute URL and nullable profile field.

Saved place example:

```json
{
  "kind": "home",
  "label": "Home",
  "address": "Mikocheni, Dar es Salaam",
  "lat": -6.7667,
  "lng": 39.2451
}
```

`kind` is `home`, `work`, or `custom`; Home and Work each have one slot. Up to 20 saved places are supported. Resolve geocoding in the client before saving.

Preference PATCH example:

```json
{
  "quiet_ride": true,
  "extra_pickup_time": false,
  "ride_updates": true,
  "promotions": false,
  "language": "sw"
}
```

Available preference fields: `quiet_ride`, `extra_pickup_time`, `share_trip_status`, `precise_location`, `ride_updates`, `trip_messages`, `promotions`, `email_enabled`, `sms_enabled`, `push_enabled` (booleans), and `language` (`en` or `sw`). These save preferences; they do not themselves provide SMS/email/push delivery or change the device locale.

Profile image uploads are rate limited to three valid images per user per hour. The API decodes and re-encodes accepted image content to PNG. Handle `429` and `Retry-After`.

## 5. Fare quote, booking, tracking, and history

### 5.1 Point and ride request types

```json
{
  "pickup": {"lat": -6.7667, "lng": 39.2451, "address": "Mikocheni, Dar es Salaam"},
  "dropoff": {"lat": -6.8167, "lng": 39.2800, "address": "Posta, Dar es Salaam"},
  "vehicle_type": "car"
}
```

### 5.2 Quote

`POST /rides/quote` requires bearer authentication and passenger role. It does not reserve a fare or a driver.

```json
{
  "estimated_fare_tzs": 8500,
  "estimated_distance_meters": 6340,
  "estimated_duration_seconds": 760,
  "distance_basis": "straight_line",
  "currency": "TZS",
  "payment_method": "cash"
}
```

The quoted estimate uses server-configured fare settings and straight-line distance. It may differ from road routing/map estimates. Show the server amount as the backend quote and explain it is an estimate.

### 5.3 Create ride

`POST /rides` requires bearer authentication, passenger role and `Idempotency-Key`. The server calculates fare and dispatches an eligible driver; do not send `fare`, `estimated_fare`, `payment_method`, driver ID, or route geometry as authoritative data.

Success: HTTP 201, ride object:

```json
{
  "id": "d42134d8-9026-424b-98e7-8ba8d01462db",
  "status": "searching",
  "passenger": {"id": "7db87b10-87ef-4e95-9c93-02e37a6f9aa1", "name": "Asha Mushi"},
  "driver": null,
  "pickup": {"lat": -6.7667, "lng": 39.2451, "address": "Mikocheni, Dar es Salaam"},
  "dropoff": {"lat": -6.8167, "lng": 39.28, "address": "Posta, Dar es Salaam"},
  "vehicle_type": "car",
  "estimated_fare": 8500,
  "created_at": "2026-10-14T12:00:00Z",
  "events": []
}
```

The `driver` is `null` until assigned; final fare and lifecycle timestamps may be absent until applicable. Current ride statuses include `searching`, `offered`, `assigned`, `driver_arrived`, `in_progress`, `completed`, `cancelled_by_passenger`, `cancelled_by_driver`, `cancelled_by_admin`, `expired`, and `no_driver_found`. Render unknown future statuses safely.

### 5.4 Ride history and detail

| Method | Path | Query/body |
| --- | --- | --- |
| GET | `/rides?limit=50&offset=0` | Optional `status`, `from`, `to` (RFC3339) |
| GET | `/rides/{rideId}` | Participant-only details and event timeline |
| GET | `/rides/{rideId}/driver-location` | Latest fresh driver point; `204` means no point |
| POST | `/rides/{rideId}/cancel` | Requires `Idempotency-Key`, body `{"reason":"Passenger changed plans"}` |
| POST | `/rides/{rideId}/rating` | After completion; body `{"score":5,"comment":"Great ride"}` |

Ride list response shape: `{"rides":[...],"total":12,"limit":50,"offset":0}`. Pagination defaults to 50, max 200. `from` and `to` must be RFC3339 and `to` must be later than `from`. Driver location is available to ride participants only while active; points older than two minutes return 204. A nonparticipant receives 404. Poll while active at a reasonable interval; respect app background restrictions.

Cancellation is state-dependent; `409` means the ride cannot be cancelled in its current state. Rating is one score from 1 to 5, with optional comment; only a participant can rate once after completion.

### 5.5 Scheduled rides

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/scheduled-rides` | Create a scheduled ride; requires `Idempotency-Key` |
| GET | `/scheduled-rides?status=upcoming&limit=50&offset=0` | List own schedules |
| GET | `/scheduled-rides/{scheduleId}` | Own schedule detail |
| POST | `/scheduled-rides/{scheduleId}/cancel` | Cancel before dispatch |

Create request:

```json
{
  "pickup": {"lat": -6.7667, "lng": 39.2451, "address": "Mikocheni"},
  "dropoff": {"lat": -6.8167, "lng": 39.28, "address": "Posta"},
  "vehicle_type": "car",
  "scheduled_for": "2026-10-15T08:30:00+03:00"
}
```

Schedule time must be 5 minutes to 30 days in the future. Status indicates persisted scheduling/dispatch outcome; `dispatched` can contain `ride_id`; `failed` can contain `failure_code` such as `no_driver_found`. A scheduled dispatch is not a driver guarantee.

## 6. Payments

The current ride payment method is **cash only**. The quote returns `payment_method: "cash"`. Do not expose card, wallet, mobile money, or other online payment options as available backend methods. Do not request provider credentials from a passenger. Local payment-provider integration is a separate backend change; until it is delivered, the passenger settles the ride fare in cash according to the in-app guidance.

## 7. Support and safety

### 7.1 Support tickets and conversation

All ticket paths below require bearer authentication and are owner-scoped for passenger app users.

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/support/tickets` | Create `{subject,description,category}`; returns 201 |
| GET | `/support/tickets?limit=50&offset=0` | List own tickets |
| GET | `/support/tickets/{ticketId}` | Ticket plus public messages and attachment metadata |
| GET | `/support/tickets/{ticketId}/messages?limit=50&offset=0` | Public conversation only |
| POST | `/support/tickets/{ticketId}/messages` | `{ "body":"..." }`; requires `Idempotency-Key` |
| POST | `/support/tickets/{ticketId}/attachments` | Multipart `file`; requires `Idempotency-Key` |
| GET | `/support/tickets/{ticketId}/attachments/{attachmentId}` | Authenticated private download |

Ticket example:

```json
{"subject":"Question about my trip","description":"Please help with my completed ride.","category":"ride"}
```

Attachments accept JPEG, PNG, or PDF up to 5 MiB, maximum 10 per ticket. Internal staff notes are never returned to the passenger. Retry message/attachment uploads with the same idempotency key after uncertain network results.

### 7.2 Emergency contacts, SOS, incident reports, and trip sharing

These routes require bearer authentication. SOS requires a real ride association and is intended for in-trip use.

| Method | Path | Purpose |
| --- | --- | --- |
| GET / POST | `/emergency-contacts` | List/create own emergency contacts |
| PUT / DELETE | `/emergency-contacts/{id}` | Update/delete own contact |
| POST | `/rides/{rideId}/sos` | Create an SOS alert associated with a ride |
| GET | `/sos/{id}` | Inspect own SOS alert |
| POST | `/sos/{id}/{action}` | Perform supported owner SOS action; action names are server-validated |
| POST / GET | `/incidents` | Create/list own incident reports |
| POST | `/incidents/{id}/attachments` | Add private evidence attachment |
| GET | `/incidents/files/{id}` | Retrieve permitted incident file |
| POST / DELETE | `/rides/{rideId}/share` | Create/revoke a ride share link |
| GET | `/share/{token}` | Public limited ride share view; token is sensitive |

The `emergency-contacts`, `incidents`, SOS and ride-share request fields are represented in the generated OpenAPI contract. Inspect those schemas before implementation rather than guessing field names. Never put an access token or refresh token into a share URL.

## 8. Promotions and referrals

| Method | Path | Body / purpose |
| --- | --- | --- |
| GET | `/promos/available?limit=50&offset=0` | List offers available to this passenger |
| POST | `/promos/validate` | Validate promo code before booking; see OpenAPI schema |
| GET | `/referral/me` | Current referral code/status |
| POST | `/referral/apply` | Apply an eligible referral code; see OpenAPI schema |

Promotions/referral do not authorize client-side fare calculation. The backend remains authoritative. Promo validation is rate limited.

## 9. Account privacy requests

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/account/deletion-request` | Request account deletion |
| GET | `/account/deletion-request` | View deletion status |
| DELETE | `/account/deletion-request` | Cancel deletion request when allowed |
| POST | `/account/export-request` | Request a data export |
| GET | `/account/export-requests?limit=50&offset=0` | List own exports |
| GET | `/account/export-requests/{id}/download` | Download own ready archive |

These endpoints have rate limits, verification requirements and a deletion grace period; use generated OpenAPI schemas for required fields and display server status. Do not imply that deletion has completed just because the request was accepted.

## 10. Errors, retry behavior, and pagination

Typical JSON error envelope:

```json
{
  "error": "Invalid request",
  "request_id": "client-or-server-correlation-id",
  "fields": {"pickup": "valid coordinates are required"},
  "code": "INVALID_REFRESH_TOKEN"
}
```

Fields may be omitted. Some legacy handlers return only `error`; parse known fields defensively. Use HTTP status as the primary category:

| Status | App behavior |
| --- | --- |
| 400 | Show/fix validation or malformed request; `fields` may identify input |
| 401 | Access credential invalid/revoked/expired; attempt refresh once for protected API calls. `INVALID_REFRESH_TOKEN` means reauthentication is required |
| 403 | Signed-in user lacks permission, required role, or account state |
| 404 | Resource not found or not visible to this user |
| 409 | State conflict (e.g. booking conflict, account-link required, invalid transition); show actionable message |
| 429 | Rate limit; honor `Retry-After`; do not rapid retry |
| 500/503 | Temporary server failure; preserve session and retry with bounded exponential backoff/jitter |
| Network timeout / no connection | Keep credentials; retry safely, reusing the same idempotency key for the same write |

For refresh specifically, `401` + `INVALID_REFRESH_TOKEN` means invalid, expired, revoked or replayed refresh credential. `503` + `AUTH_TEMPORARILY_UNAVAILABLE` is transient: keep the refresh token and retry later. Never log request bodies on authentication routes. Never log passwords, OTPs, Google ID tokens, access tokens, refresh tokens or share-link tokens.

Paginated collection endpoints accept `limit` and `offset`; default `limit=50`, maximum `200`, minimum `1`, `offset >= 0`. Follow the returned `total`, `limit` and `offset` rather than assuming a fixed page size.

## 11. Android Retrofit / OkHttp implementation notes

Suggested Retrofit methods (illustrative):

```kotlin
interface RidaApi {
    @POST("auth/login") suspend fun login(@Body body: LoginRequest): SessionResponse
    @POST("auth/register") suspend fun register(@Body body: RegisterRequest): SessionResponse
    @POST("auth/google/challenge") suspend fun googleChallenge(): GoogleChallenge
    @POST("auth/google") suspend fun googleExchange(@Body body: GoogleExchangeRequest): SessionResponse
    @POST("auth/refresh") suspend fun refresh(@Body body: RefreshRequest): SessionResponse
    @GET("me") suspend fun me(): MeResponse
    @POST("rides/quote") suspend fun quote(@Body body: RideRequest): RideQuote
    @POST("rides") suspend fun createRide(
        @Header("Idempotency-Key") key: String,
        @Body body: RideRequest
    ): RideResponse
}
```

Configure Retrofit base URL as `https://rida-backend.ditronics.co.tz/api/v1/` (trailing slash required). Use Moshi adapters that tolerate nullable fields and unknown response fields. Use an OkHttp authenticator/interceptor with a coroutine-safe single-flight refresh coordinator; avoid doing a blocking refresh from an interceptor on the same dispatcher that is required to execute it. Prevent recursive refresh requests. Retry the original request once only. Do not log Authorization headers; disable BODY logging in production.

Recommended client layers:

1. `AuthApi` on an unauthenticated OkHttp client for login, Google exchange and refresh.
2. Protected API client attaches the current Rida access token.
3. Session repository serializes refresh and atomically persists replacement token pair.
4. Data/repository layer turns 204 into an empty optional result (e.g. no driver location).
5. UI handles `onboardingRequired`, transient errors, rate limits, ride state conflicts and cash payment state explicitly.

## 12. Environment / deployment facts relevant to Android

The mobile client needs only the public API base URL and its Google Android OAuth configuration. It must not contain backend secrets, Google web client secret, JWT secrets, database passwords, or provider credentials. The backend uses `GOOGLE_WEB_CLIENT_ID` for ID-token audience verification. The Android Google sign-in request uses the configured Web client ID as the server client ID; do not substitute the Android OAuth client ID as the token audience.

Server API schema changes require applying the backend's versioned migrations before deploying the corresponding API build. Android should read the actual API health route (`/api/v1/ready`) in diagnostics; do not use `/login` as a web URL.

## 13. Current implementation boundaries

- Ride fares are estimated/finalized by server configuration; client estimates are informational only.
- Cash is the only supported ride payment option.
- Phone verification delivery needs a configured SMS provider. UI must handle provider-unavailable errors.
- Google sign-in requires backend Google Web client ID configuration and the nonce challenge flow.
- Push/SMS/email delivery preferences are stored settings; they do not promise a delivery provider is connected.
- Scheduled rides trigger a dispatch attempt at the scheduled time; a driver is not guaranteed.
- Some safety and promotion request schemas are intentionally delegated to the current generated OpenAPI document linked above.

## 14. Source of truth and keeping this document current

When implementation and this guide disagree, inspect the route/controller and the generated [`docs/api/openapi.yaml`](docs/api/openapi.yaml) for the deployed commit. Do not infer a request schema from a screen mockup. Update this guide and regenerate the OpenAPI contract in the same backend change whenever mobile-facing endpoints or payloads change.
