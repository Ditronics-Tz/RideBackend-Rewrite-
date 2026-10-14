# Passenger Android authentication

All paths use the existing `/api/v1` prefix. Requests and responses are JSON over HTTPS. The Android application ID is `com.example.ridepassenger2`; configure its signing certificate in Google Cloud and request an ID token for the **Web** OAuth client ID. Set that ID in `GOOGLE_WEB_CLIENT_ID` on the backend. The Android app must not contain a Google client secret.

Run `go run ./cmd/migrate up` before starting the updated API. Migration 10 adds a durable, one-use Google challenge table and an absolute deadline to existing app sessions. Existing refresh sessions retain their previous inactivity expiry; the new 90-day policy applies to newly issued sessions and later refreshes, bounded by the 365-day deadline. Browser redirect OAuth (`GET /api/v1/auth/google`) remains separate and uses `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and the existing redirect allowlist.

## Session response

`POST /auth/register`, `POST /auth/login`, `POST /auth/google`, and `POST /auth/refresh` all return this shape. Registration returns 201; the others return 200.

```json
{
  "user": {"id":"uuid","name":"Asha","email":"asha@example.com","phone":null,"role":"passenger","status":"active","is_active":true,"phone_verified_at":null,"suspended_until":null},
  "accessToken":"eyJ...",
  "expiresIn":900,
  "refreshToken":"opaque-random-token",
  "sessionId":"uuid",
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

`user.id` and `sessionId` are UUID strings. `name`, `email`, `role`, and `status` are strings; `phone` is a string or JSON null; `is_active` and `restricted` are booleans; `phone_verified_at` and `suspended_until` are RFC 3339 timestamps or null. `expiresIn`/`expires_in` are integer seconds, configurable with a default of 900. `onboardingRequired` is an array of strings, empty for a passenger with a verified phone; `phone_verification` means phone verification is incomplete. Current ride booking has no server phone-verification gate. A real SMS sender must be configured before the existing `/auth/phone/request-otp` and `/auth/phone/verify-otp` flow can finish onboarding. The snake_case fields are retained for existing clients.

Store the refresh token in Android encrypted storage. Send the access token as `Authorization: Bearer <accessToken>`. `X-Device-ID` and `X-Device-Platform: android` are optional informational metadata, never proof of identity. Access tokens expire after 15 minutes by default. Refresh inactivity expires after 90 days without a successful refresh; every refresh slides that deadline but never past 365 days from the original session creation. Rotation also replaces `sessionId`, so the prior access token stops working immediately. Access tokens are checked against the session database on every protected request: logout, logout-all, reuse detection, and administrative revocation take effect on the next request after the revocation transaction commits.

## Requests

On the Docker VPS, apply migration 10 with `docker compose --profile tools run --rm migrate up` before recreating the API service.

`POST /api/v1/auth/register`:

```json
{"name":"Asha","email":"asha@example.com","password":"a-unique-long-password"}
```

New registrations default to passenger. Existing driver registration remains supported through the optional `role` field. Passwords use bcrypt and the existing 10-character minimum/72-byte maximum policy.

`POST /api/v1/auth/login` accepts an email or a **verified** Tanzanian phone number. Use `identifier` for new clients; `email` and `phone` remain accepted aliases. Phone values are normalized to `+255...` before lookup. Unknown account and wrong password both return a generic 401.

```json
{"identifier":"0712 345 678","password":"a-unique-long-password"}
```

`POST /api/v1/auth/google/challenge` accepts `{}` and returns:

```json
{"challengeId":"uuid","nonce":"64-hex-character-random-value","expiresAt":"2026-10-14T12:05:00Z"}
```

Pass `nonce` to the Google ID token request. Within five minutes, call `POST /api/v1/auth/google`:

```json
{"idToken":"google-signed-id-token","challengeId":"uuid"}
```

The backend validates Google's signature and certificate, Web client audience, issuer, expiry, verified email, and the challenge nonce. It consumes the challenge atomically after token verification. Accounts are looked up by Google `sub` and provider `google`. An existing password account with the same email returns 409 `ACCOUNT_LINK_REQUIRED`; automatic email matching never links it. Authenticated account linking is not yet exposed. A new Google account can have `phone: null`.

`POST /api/v1/auth/refresh` accepts either `{"refreshToken":"opaque-random-token"}` or the legacy `{"refresh_token":"opaque-random-token"}`. It returns a new session response. The old refresh token is invalidated, and replay revokes its entire device token family. Allow only one refresh in flight per stored token. A lost refresh response is ambiguous: keep the local token on a network error, but avoid parallel or blind replay; a later 401 means the session must be reauthenticated.

`GET /api/v1/me` returns `{"user":{...},"onboardingRequired":[]}` using the `MobileUser` shape above. The existing `GET /api/v1/auth/me` remains available with profile and rating fields.

`POST /api/v1/auth/logout` and `POST /api/v1/auth/logout-all` require the current access token and return `{"message":"Logged out"}` or `{"message":"All sessions revoked"}`. Logout revokes the current refresh session; logout-all revokes all of the account's sessions.

## Error handling

| HTTP | `code` | Android action |
| --- | --- | --- |
| 401 | `INVALID_REFRESH_TOKEN` | Discard session and ask for sign-in. Covers malformed, expired, revoked, or reused refresh credentials. |
| 503 | `AUTH_TEMPORARILY_UNAVAILABLE` | Keep credentials and retry later. |
| 401 | `GOOGLE_TOKEN_INVALID`, `GOOGLE_CHALLENGE_INVALID` | Obtain a fresh Google challenge and ID token. |
| 409 | `ACCOUNT_LINK_REQUIRED` | Ask user to sign in with the existing password account; do not create or link silently. |
| 503 | `GOOGLE_NOT_CONFIGURED` | Google sign-in is unavailable until backend configuration is supplied. |
| 429 | existing rate-limit response | Respect `Retry-After`; keep credentials. |

Other validation errors include `error` and sometimes `fields`; requests are tagged with `request_id`. The API never logs passwords, ID tokens, access tokens, or refresh tokens. Configure `GOOGLE_WEB_CLIENT_ID`, `APP_ACCESS_TTL`, `APP_REFRESH_INACTIVITY_TTL`, and `APP_REFRESH_ABSOLUTE_TTL` in the server environment. Defaults are `15m`, `2160h`, and `8760h`. The Android app contains only the public Web client ID. See the generated [OpenAPI contract](openapi.yaml) for all fields and routes.
