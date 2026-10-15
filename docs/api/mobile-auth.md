# Passenger Android authentication

For the complete Android Credential Manager flow, Google Cloud setup, backend account persistence, and displaying the saved full name and email in Profile, see [Google sign-in and Profile integration](passenger-google-sign-in-android.md).

All paths use the existing `/api/v1` prefix. Requests and responses are JSON over HTTPS. The Android application ID is `com.example.ridepassenger2`; configure its signing certificate in Google Cloud and request an ID token for the **Web** OAuth client ID. Set that ID in `GOOGLE_WEB_CLIENT_ID` on the backend. The Android app must not contain a Google client secret.

Run `go run ./cmd/migrate up` before starting the updated API. Migration 12 makes `users.name` nullable without changing existing values. Migration 10 added a durable, one-use Google challenge table and an absolute deadline to existing app sessions. Existing refresh sessions retain their previous inactivity expiry; the new 90-day policy applies to newly issued sessions and later refreshes, bounded by the 365-day deadline. Browser redirect OAuth (`GET /api/v1/auth/google`) remains separate and uses `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and the existing redirect allowlist.

## Session response

`POST /auth/register`, `POST /auth/login`, `POST /auth/google`, and `POST /auth/refresh` all return this shape. Registration returns 201; the others return 200.

```json
{
  "user": {"id":"d2a2f6ec-7b8c-4eca-afec-2bd4f9b95220","name":"","full_name":null,"email":"asha@example.com","phone":null,"phone_number":null,"profile_completed":false,"role":"passenger","status":"active","is_active":true,"phone_verified_at":null,"suspended_until":null},
  "accessToken":"eyJ...",
  "expiresIn":900,
  "refreshToken":"opaque-random-token",
  "sessionId":"1746f12d-c7aa-45d0-8403-d02d1e334f4a",
  "onboardingRequired":["phone_verification"],
  "message":"Login successful",
  "token":"eyJ...",
  "access_token":"eyJ...",
  "expires_in":900,
  "refresh_token":"opaque-random-token",
  "refresh_expires_at":"2027-01-12T12:00:00Z",
  "restricted":false
}
```

`user.id` and `sessionId` are UUID strings. `name` is a legacy string that is empty until set in Profile; `full_name` is a string or null. `email`, `role`, and `status` are strings; `phone` and `phone_number` are strings or JSON null. `profile_completed` is true once both name and phone are present, even while phone verification is pending. `is_active` and `restricted` are booleans; `phone_verified_at` and `suspended_until` are RFC 3339 timestamps or null. `accessToken` is a signed Rida JWT string, `refreshToken` is an opaque string, and `refresh_expires_at` is an RFC 3339 timestamp. `expiresIn`/`expires_in` are integer seconds, configurable with a default of 900. `onboardingRequired` is an array of strings, empty for a passenger with a verified phone; `phone_verification` means phone verification is incomplete. Current ride booking has no server phone-verification gate. A real SMS sender must be configured before the existing `/auth/phone/request-otp` and `/auth/phone/verify-otp` flow can finish onboarding. The snake_case fields are retained for existing clients.

Store the refresh token in Android encrypted storage. Send the access token as `Authorization: Bearer <accessToken>`. `X-Device-ID` and `X-Device-Platform: android` are optional informational metadata, never proof of identity. Access tokens expire after 15 minutes by default. Refresh inactivity expires after 90 days without a successful refresh; every refresh slides that deadline but never past 365 days from the original session creation. Rotation also replaces `sessionId`, so the prior access token stops working immediately. Access tokens are checked against the session database on every protected request: logout, logout-all, reuse detection, and administrative revocation take effect on the next request after the revocation transaction commits.

## Requests

On the Docker VPS, apply pending migrations through version 12 with `docker compose --profile tools run --rm migrate up` before recreating the API service.

`POST /api/v1/auth/register` (201; response is the session JSON above):

```json
{"email":"asha@example.com","password":"a-unique-long-password"}
```

New registrations default to passenger. Existing driver registration remains supported through the optional `role` field. Name and phone are entered later through Profile. Passwords use bcrypt and the existing 10-character minimum/72-byte maximum policy.

`POST /api/v1/auth/login` (200; response is the session JSON above) accepts email and password only. Email is trimmed and lowercased. A phone-only request is rejected with the same generic 401 used for invalid credentials.

```json
{"email":"asha@example.com","password":"a-unique-long-password"}
```

`POST /api/v1/auth/google/challenge` accepts `{}` with no Bearer token and returns 200:

```json
{"challengeId":"uuid","nonce":"64-hex-character-random-value","expiresAt":"2026-10-14T12:05:00Z"}
```

`challengeId` is a UUID string, `nonce` is a random 64-character hex string, and `expiresAt` is an RFC 3339 timestamp. Pass `nonce` to the Google ID token request. Within five minutes, call `POST /api/v1/auth/google` (200; response is the session JSON above):

```json
{"idToken":"google-signed-id-token","challengeId":"uuid"}
```

The backend validates Google's signature and certificate, Web client audience, issuer, expiry, verified email, and the challenge nonce. It consumes the challenge atomically after token verification. Accounts are looked up by Google `sub` and provider `google`. An existing password account with the same email returns 409 `ACCOUNT_LINK_REQUIRED`; automatic email matching never links it. Authenticated account linking is not yet exposed. A new Google account can have `phone: null`.

`POST /api/v1/auth/refresh` accepts either `{"refreshToken":"opaque-random-token"}` or the legacy `{"refresh_token":"opaque-random-token"}`. It returns 200 with a new session response. The old refresh token and session ID are invalidated, and replay revokes its entire device token family. Allow only one refresh in flight per stored token. A lost refresh response is ambiguous: keep the local token on a network error, but avoid parallel or blind replay; a later 401 means the session must be reauthenticated.

`GET /api/v1/me` requires `Authorization: Bearer <accessToken>` and has no request body. It returns 200 using the same `MobileUser` shape as the session response:

```json
{"user":{"id":"d2a2f6ec-7b8c-4eca-afec-2bd4f9b95220","name":"","full_name":null,"email":"asha@example.com","phone":null,"phone_number":null,"profile_completed":false,"role":"passenger","status":"active","is_active":true,"phone_verified_at":null,"suspended_until":null},"onboardingRequired":["phone_verification"]}
```

The existing `GET /api/v1/auth/me` also requires a Bearer token and no body. It returns the legacy account detail contract, including `average_rating` (number), `total_ratings` (integer), and optional `passenger_profile` or `driver_profile` objects. Its `user.phone` is an empty string when unset, so use `/api/v1/me` for the nullable-phone mobile contract. Example:

```json
{"user":{"id":"d2a2f6ec-7b8c-4eca-afec-2bd4f9b95220","name":"Asha","email":"asha@example.com","role":"passenger","is_active":true,"status":"active","suspended_until":null,"phone":"","phone_verified_at":null},"average_rating":0,"total_ratings":0}
```

`POST /api/v1/auth/logout` and `POST /api/v1/auth/logout-all` require the current Bearer token and accept no request body. They return 200 with `{"message":"Logged out"}` or `{"message":"All sessions revoked"}` respectively. Logout revokes the current device session; logout-all revokes all of the account's sessions. Clear local credentials after a successful response.

### Passenger profile after sign-in

`GET /api/v1/passenger/profile/me` returns the existing passenger profile object. A new email-only account has `full_name: ""` and `phone: ""` in this legacy profile shape. The compact `/api/v1/me` user object gives nullable `full_name` and `phone_number` and a `profile_completed` boolean.

`PUT /api/v1/passenger/profile/me` accepts either field independently; it returns the updated passenger profile object:

```json
{"full_name":"Asha Mwakalinga","phone_number":"0712 345 678"}
```

The server stores that phone as `+255712345678`, rejects a duplicate with 409, and clears `phone_verified_at` if the number changes. Entering a phone does **not** verify ownership. The existing authenticated `POST /api/v1/auth/phone/request-otp` and `POST /api/v1/auth/phone/verify-otp` flow verifies it when a real SMS sender is configured. The legacy `phone` field is still accepted by the profile update endpoint. Registration and login never require profile completion.

## Error handling

| HTTP | `code` | Android action |
| --- | --- | --- |
| 401 | `INVALID_REFRESH_TOKEN` | Discard session and ask for sign-in. Covers malformed, expired, revoked, or reused refresh credentials. |
| 503 | `AUTH_TEMPORARILY_UNAVAILABLE` | Keep credentials and retry later. |
| 401 | `GOOGLE_TOKEN_INVALID`, `GOOGLE_CHALLENGE_INVALID` | Obtain a fresh Google challenge and ID token. |
| 409 | `ACCOUNT_LINK_REQUIRED` | Ask user to sign in with the existing password account; do not create or link silently. |
| 503 | `GOOGLE_NOT_CONFIGURED` | Google sign-in is unavailable until backend configuration is supplied. |
| 429 | existing rate-limit response | Respect `Retry-After`; keep credentials. |

Error body example: `{"error":"invalid refresh credential","code":"INVALID_REFRESH_TOKEN","request_id":"request-id"}`. `error` is a human-readable string, `code` is a machine-readable string when provided, `request_id` is a trace identifier, and validation errors may include `fields`. Do not parse `error` for app state. A protected endpoint can return 401 for an expired access token; try one serialized refresh. If refresh returns 401 `INVALID_REFRESH_TOKEN`, require sign-in. Keep stored credentials on network errors, 429, 500, and 503.

Other validation errors include `error` and sometimes `fields`; requests are tagged with `request_id`. The API never logs passwords, ID tokens, access tokens, or refresh tokens. Configure `GOOGLE_WEB_CLIENT_ID`, `APP_ACCESS_TTL`, `APP_REFRESH_INACTIVITY_TTL`, and `APP_REFRESH_ABSOLUTE_TTL` in the server environment. Defaults are `15m`, `2160h`, and `8760h`. The Android app contains only the public Web client ID. See the generated [OpenAPI contract](openapi.yaml) for all fields and routes.

## Passenger Android integration

Configure the Android app `com.example.ridepassenger2` in Google Cloud with the signing certificate for each build variant. Use the Web OAuth client ID as the ID-token audience. Google Cloud credentials and the backend `GOOGLE_WEB_CLIENT_ID` must refer to that same Web client. The Android app does not use a Google client secret. The server's browser OAuth `GOOGLE_CLIENT_SECRET` is unrelated to this flow. Rotate any client secret that was pasted into a chat or other untrusted location.

With Retrofit, expose the routes above as suspend calls using Moshi DTOs matching the JSON property names exactly (`idToken`, `challengeId`, `refreshToken`, `accessToken`, `expiresIn`, `sessionId`, `onboardingRequired`). Represent `user.phone` and the two user timestamps as nullable. Keep the refresh endpoint on a Retrofit/OkHttp client without the auth interceptor to avoid recursive refresh. A compact interface looks like this:

```kotlin
interface PassengerAuthApi {
    @POST("api/v1/auth/google/challenge") suspend fun challenge(@Body body: Map<String, String> = emptyMap()): ChallengeDto
    @POST("api/v1/auth/google") suspend fun google(@Body body: GoogleExchangeDto): SessionDto
    @POST("api/v1/auth/refresh") suspend fun refresh(@Body body: RefreshDto): SessionDto
    @GET("api/v1/me") suspend fun me(@Header("Authorization") bearer: String): MeDto
    @POST("api/v1/auth/logout") suspend fun logout(@Header("Authorization") bearer: String): MessageDto
    @POST("api/v1/auth/logout-all") suspend fun logoutAll(@Header("Authorization") bearer: String): MessageDto
}

data class GoogleExchangeDto(val idToken: String, val challengeId: String)
data class RefreshDto(val refreshToken: String)
```

1. On a fresh Google sign-in, request a challenge, request a Google ID token with that exact nonce and the Web client ID, then exchange the ID token plus `challengeId`. Never send a Google access token or authorization code to this endpoint.
2. Persist the latest Rida refresh token in Android secure storage and keep the access token in memory. On app launch, try refresh with the stored token before showing sign-in. A successful refresh replaces both tokens and the session ID; save the new refresh token before sending further authenticated calls.
3. Attach `Authorization: Bearer <accessToken>` to protected calls. Serialize refresh attempts across requests and retry a failed protected call at most once with the new access token. A 401 on the refresh call ends the session; a timeout or server failure does not.
4. For sign-out, call logout or logout-all while the access token is valid, then erase local tokens after success. If the access token has expired, refresh first. Device ID/platform headers may help display sessions but never replace credentials.
5. Handle `onboardingRequired` explicitly. A new Google passenger has `phone: null` and `phone_verification` until the existing phone OTP flow succeeds; the server currently does not block booking based on that marker.

In production use an HTTPS API URL, with the reverse proxy enforcing TLS and forwarding only to the loopback-bound backend. Do not permit cleartext traffic in the Android release build. For local development, start PostgreSQL, set the required values in a private environment, run `go run ./cmd/migrate up`, then `go run .`; the local HTTP URL is only for development. For the Ubuntu VPS, follow [Docker deployment](../../DOCKER_DEPLOYMENT.md): set the Web client ID in `.env`, run `docker compose config -q`, `docker compose build`, `docker compose up -d db`, `docker compose --profile tools run --rm migrate up`, and `docker compose up -d api`. Inspect `docker compose ps` and `docker compose logs -f --tail=100 api` before pointing the Android release build at the HTTPS domain.
