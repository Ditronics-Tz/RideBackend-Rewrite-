# Rida passenger Android: Google sign-in and Profile

**Audience:** Android developer for `com.example.ridepassenger2`  
**API base URL:** `https://rida-backend.ditronics.co.tz/`  
**Backend contract:** `/api/v1` routes in this guide  
**Updated:** 15 October 2026

This guide connects Android Credential Manager to the existing Rida backend. Google authenticates the person and returns a signed **ID token**. Rida verifies it, creates or finds a Rida user in PostgreSQL, and returns **Rida-owned** access and refresh tokens. The Profile screen reads the Rida user. Do not treat the profile fields on the Google credential as the saved Rida profile.

## What already exists in the backend

| Component | Current behavior |
| --- | --- |
| `POST /api/v1/auth/google/challenge` | Creates a random, single-use nonce and challenge ID that expire after five minutes. The database stores the nonce hash. |
| `POST /api/v1/auth/google` | Verifies Google's ID token, Web client audience, issuer, expiry, subject, verified email, and signed nonce; consumes the challenge; creates or finds a Rida account; issues a Rida session. |
| `users` | Stores the Rida account ID, verified Google email and, for a **new** account, Google's display name if the token supplies one. Name may be empty. Phone is not invented. |
| `oauth_identities` | Stores the unique `provider=google` plus Google `sub` mapping to the Rida user ID. The email is **not** used as the Google account identifier. |
| `passenger_profiles` | Stores the initial full name (if supplied) and later Profile edits. |
| `app_sessions` | Stores per-device Rida refresh sessions. The app can resume without asking Google again on every launch. |

All of the account creation and identity mapping above happens inside the existing backend flow. The Android app must never submit a claimed full name or email as proof of identity. Google may omit `name`; in that case, Rida leaves the name empty and the Profile screen prompts the passenger to fill it in. For an already linked Google account, signing in again returns the same Rida user and does **not** overwrite a name the passenger edited in Profile. The stored Rida email also remains the value returned by `/api/v1/me`; Google account email changes are not automatically copied into Rida.

An existing password account with the same email is **not** linked automatically. The exchange returns `409 ACCOUNT_LINK_REQUIRED`. That user can sign in with email/password; authenticated Google account linking is not currently exposed by the backend.

## Google Cloud and Android setup

1. In the Rida Google Cloud project, configure the Google Auth Platform consent/branding screen. Add test users while the app is in testing mode. Complete production branding and verification requirements before broad release.
2. Create an **Android** OAuth client for the exact application ID `com.example.ridepassenger2` and the SHA-1 of each certificate used to distribute that variant. Register debug and release signing certificates separately when they differ. For a Play-distributed app, use the **Play app signing** certificate SHA-1 from Play Console; the upload key can differ.
3. Create or select a **Web application** OAuth client in the same project. Its **client ID** is the value used in Android's `setServerClientId(...)` and in the backend's `GOOGLE_WEB_CLIENT_ID`. These values must match exactly.
4. Set `GOOGLE_WEB_CLIENT_ID` in the backend server's private `.env`, then restart the API if changing it. The mobile app may contain this **public client ID**. It must never contain the Google **client secret**, Rida JWT secret, or a server refresh token.
5. Use an HTTPS Retrofit base URL ending in `/`. The Android release build must not allow cleartext API traffic.

Get a local certificate fingerprint with `./gradlew signingReport` in the Android project (or `gradlew signingReport` on Windows). Check the actual variant that users install. [Google's Android client-auth guide](https://developers.google.com/android/guides/client-auth) explains debug, release, and Play signing fingerprints.

The Rida browser OAuth redirect endpoints (`GET /api/v1/auth/google` and its callback) use separate configuration. The passenger phone follows the **ID-token exchange** below; it does not open the browser callback or send a Google authorization code to `/api/v1/auth/google`.

### Android dependencies

The following were the stable Credential Manager and Google ID SDK versions listed in official release notes on 15 October 2026. Keep the project's Gradle version catalog consistent if it already manages versions.

```kotlin
dependencies {
    implementation("androidx.credentials:credentials:1.6.0")
    implementation("androidx.credentials:credentials-play-services-auth:1.6.0")
    implementation("com.google.android.libraries.identity.googleid:googleid:1.2.1")
    // Existing Retrofit, OkHttp, Moshi and Compose dependencies remain in use.
}
```

Use the project's existing `google()` Maven repository. Android's [Credential Manager sign-in guide](https://developer.android.com/identity/sign-in/credential-manager-siwg-implementation) is the upstream reference for the API calls.

## Exact sign-in sequence on the phone

```text
Passenger taps "Sign in with Google"
  -> POST /api/v1/auth/google/challenge with {}
  <- challengeId, nonce, expiresAt
  -> Credential Manager requests a Google ID token with:
       serverClientId = Rida Web OAuth client ID
       nonce = exact nonce returned by Rida
  <- signed Google ID token
  -> POST /api/v1/auth/google with {idToken, challengeId}
  <- Rida user, accessToken, refreshToken, sessionId
  -> securely save latest Rida refresh token
  -> show Profile from Rida response or GET /api/v1/me
```

The challenge lasts **five minutes** and works only once. Start with a **new challenge for each attempt**. If the user cancels Google UI or the challenge expires, discard it. Pass Rida's nonce string **unchanged** to Credential Manager's `setNonce(...)`; do not generate a second nonce, hash it, or send it as a separate field to the exchange endpoint. Rida checks the nonce claim inside Google's signed ID token.

### Backend requests and responses

```http
POST /api/v1/auth/google/challenge
Content-Type: application/json

{}
```

```json
{
  "challengeId": "e87f2a5e-d28f-4c84-9f11-b70e99df97eb",
  "nonce": "64-random-hex-characters-from-the-server-replace-this-example",
  "expiresAt": "2026-10-15T12:05:00Z"
}
```

`challengeId` is a UUID string; `nonce` is exactly 64 hexadecimal characters in a real response; `expiresAt` is an RFC 3339 timestamp. The example nonce above is descriptive, not a usable challenge.

```http
POST /api/v1/auth/google
Content-Type: application/json

{"idToken":"GOOGLE_SIGNED_ID_TOKEN","challengeId":"e87f2a5e-d28f-4c84-9f11-b70e99df97eb"}
```

Successful exchange returns HTTP 200. These are the fields the Android app should use; the response also retains legacy snake_case token aliases for older clients:

```json
{
  "user": {
    "id": "c9e38afc-6677-43b8-9e24-8bb612a9c1a8",
    "name": "Asha Mwakalinga",
    "full_name": "Asha Mwakalinga",
    "email": "asha@example.com",
    "phone": null,
    "phone_number": null,
    "profile_completed": false,
    "role": "passenger",
    "status": "active",
    "is_active": true,
    "phone_verified_at": null,
    "suspended_until": null
  },
  "accessToken": "RIDA_ACCESS_TOKEN",
  "expiresIn": 900,
  "refreshToken": "RIDA_OPAQUE_REFRESH_TOKEN",
  "sessionId": "497f9533-c1fe-4f1a-a0df-5d92918c9fb1",
  "onboardingRequired": ["phone_verification"]
}
```

`full_name`, `phone`, and `phone_number` can be JSON `null`. Google may supply no name, so `name` can be the legacy empty string and `full_name` can be `null`. Never construct the Profile screen from `idToken` contents or local Google credential claims after exchange; use the Rida `user` object or a fresh `/me` response.

## Kotlin integration example

The snippet fits a Compose app using Retrofit, Moshi, and coroutines. Connect the DTOs to the project's existing Moshi adapter setup and session storage. Keep the Google Web client ID in a normal Android resource or build configuration value; it is public, while the client secret is server-only.

```kotlin
import android.app.Activity
import androidx.credentials.CredentialManager
import androidx.credentials.CustomCredential
import androidx.credentials.GetCredentialRequest
import com.google.android.libraries.identity.googleid.GetSignInWithGoogleOption
import com.google.android.libraries.identity.googleid.GoogleIdTokenCredential
import com.squareup.moshi.Json
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.PUT

data class GoogleChallengeDto(
    val challengeId: String,
    val nonce: String,
    val expiresAt: String,
)
data class GoogleExchangeDto(val idToken: String, val challengeId: String)
data class RefreshDto(val refreshToken: String)
data class ProfileUpdateDto(
    @Json(name = "full_name") val fullName: String? = null,
    @Json(name = "phone_number") val phoneNumber: String? = null,
)
data class RidaUserDto(
    val id: String,
    val name: String,
    @Json(name = "full_name") val fullName: String?,
    val email: String,
    @Json(name = "phone_number") val phoneNumber: String?,
    @Json(name = "profile_completed") val profileCompleted: Boolean,
    @Json(name = "phone_verified_at") val phoneVerifiedAt: String?,
)
data class RidaSessionDto(
    val user: RidaUserDto,
    val accessToken: String,
    val expiresIn: Int,
    val refreshToken: String,
    val sessionId: String,
    val onboardingRequired: List<String>,
)
data class RidaMeDto(val user: RidaUserDto, val onboardingRequired: List<String>)
data class PassengerProfileDto(
    @Json(name = "full_name") val fullName: String,
    val phone: String,
    @Json(name = "profile_completed") val profileCompleted: Boolean,
)

interface RidaAuthApi {
    @POST("api/v1/auth/google/challenge")
    suspend fun challenge(@Body body: Map<String, String> = emptyMap()): GoogleChallengeDto

    @POST("api/v1/auth/google")
    suspend fun google(@Body body: GoogleExchangeDto): RidaSessionDto

    @POST("api/v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshDto): RidaSessionDto

    @GET("api/v1/me")
    suspend fun me(@Header("Authorization") bearer: String): RidaMeDto

    @PUT("api/v1/passenger/profile/me")
    suspend fun updateProfile(
        @Header("Authorization") bearer: String,
        @Body body: ProfileUpdateDto,
    ): PassengerProfileDto
}

// Call from a foreground Activity while its lifecycle is active.
suspend fun signInWithGoogle(
    activity: Activity,
    api: RidaAuthApi,
    webClientId: String,
): RidaSessionDto {
    val challenge = api.challenge()
    val googleOption = GetSignInWithGoogleOption.Builder(webClientId)
        .setNonce(challenge.nonce)
        .build()
    val request = GetCredentialRequest.Builder()
        .addCredentialOption(googleOption)
        .build()
    val result = CredentialManager.create(activity).getCredential(
        context = activity,
        request = request,
    )
    val custom = result.credential as? CustomCredential
        ?: error("Google did not return an ID token credential")
    require(custom.type == GoogleIdTokenCredential.TYPE_GOOGLE_ID_TOKEN_CREDENTIAL) {
        "Unexpected credential type"
    }
    val idToken = GoogleIdTokenCredential.createFrom(custom.data).idToken
    return api.google(GoogleExchangeDto(idToken, challenge.challengeId))
}
```

The `PassengerProfileDto` above includes fields used by this flow; Moshi can ignore additional fields in the backend response. Do not log `idToken`, Rida tokens, HTTP Authorization headers, or full auth response bodies in OkHttp/Logcat. The manual button option above is appropriate for an explicit Compose "Sign in with Google" button. If the app also offers the Credential Manager bottom sheet, use `GetGoogleIdOption` with **a nonce from a new server challenge**; do not reuse a challenge between the button and bottom sheet.

In a Compose `onClick`, launch this suspend flow through the screen's coroutine scope or ViewModel. Show a loading state, handle user cancellation without marking it as a server failure, save the **Rida** refresh token when the exchange succeeds, then navigate to the authenticated area. Make sure the Activity context used for `getCredential(...)` belongs to the visible foreground screen.

## Show the saved full name and email in Profile

Use the `user` object from the Rida session response immediately after login. On opening Profile later, call `GET /api/v1/me` with `Authorization: Bearer <accessToken>`. The backend returns the saved Rida email and name:

```http
GET /api/v1/me
Authorization: Bearer RIDA_ACCESS_TOKEN
```

```json
{
  "user": {
    "id": "c9e38afc-6677-43b8-9e24-8bb612a9c1a8",
    "name": "Asha Mwakalinga",
    "full_name": "Asha Mwakalinga",
    "email": "asha@example.com",
    "phone": null,
    "phone_number": null,
    "profile_completed": false
  },
  "onboardingRequired": ["phone_verification"]
}
```

The real response contains additional user fields. `GET /api/v1/passenger/profile/me` supplies the wider passenger profile (including profile image fields), but its legacy `full_name` and `phone` values are empty strings when missing and it does **not** supply email. Use `/api/v1/me` as the simple source for the Profile header's name and email.

```kotlin
@Composable
fun ProfileHeader(user: RidaUserDto, onEditProfile: () -> Unit) {
    val displayName = user.fullName?.takeIf { it.isNotBlank() } ?: "Add your full name"
    Column {
        Text(text = displayName)
        Text(text = user.email) // Stored Rida account email; display as read-only.
        if (user.fullName.isNullOrBlank() || user.phoneNumber.isNullOrBlank()) {
            TextButton(onClick = onEditProfile) { Text("Complete profile") }
        }
    }
}
```

Add the normal Compose imports (`Composable`, `Column`, `Text`, `TextButton`) from the app's UI libraries. A Google profile picture is **not** automatically imported into Rida by this backend; use the existing profile photo flow if the app needs a saved picture.

To edit the saved name, call `PUT /api/v1/passenger/profile/me` with `{"full_name":"Asha Mwakalinga"}`. To add a phone, send `{"phone_number":"0712345678"}`; the backend stores `+255712345678` and rejects duplicates. Phone entry is **not** phone verification. The existing OTP endpoints handle verification when an SMS sender is configured. `profile_completed` means a name and phone are present; `phone_verified_at` and `onboardingRequired` separately show verification state. There is no profile email-edit endpoint in this contract; display the account email read-only.

## Returning users, token refresh, and logout

1. Save the latest **Rida refresh token** in storage protected by Android Keystore. Keep the short-lived access token in memory where possible. Store the Rida user ID for UI convenience only; obtain authoritative profile fields from `/me`.
2. On app launch, if a Rida refresh token exists, call `POST /api/v1/auth/refresh` with `{"refreshToken":"..."}` before showing the Google sign-in screen. The response replaces **both** the refresh token and `sessionId`; persist the new refresh token before launching other protected requests.
3. Send `Authorization: Bearer <accessToken>` to protected routes. Access tokens default to 15 minutes. The refresh inactivity limit defaults to 90 days with a 365-day absolute maximum. Serialize refresh calls: replay of a rotated refresh token revokes its token family.
4. If an access-token request returns 401, perform at most one serialized refresh and retry once. A refresh response of 401 `INVALID_REFRESH_TOKEN` requires sign-in again. Network failure, 429, 500, or 503 does **not** mean the passenger is logged out; retain credentials. If a refresh response was lost after the server rotated its token, repeating the old refresh token can revoke that device's token family. Avoid concurrent or blind retries; a subsequent explicit 401 requires fresh sign-in.
5. On explicit logout, call `POST /api/v1/auth/logout` with the current Rida access token, remove local Rida credentials after success, and call Credential Manager's `clearCredentialState()` so the next Google sign-in can show all available accounts. `POST /api/v1/auth/logout-all` revokes every device session.

Google sign-in is therefore needed for the first session or after the Rida refresh session is invalid. It should **not** run on every app start.

## Errors the phone must handle

| HTTP | `code` | Android behavior |
| --- | --- | --- |
| 401 | `GOOGLE_TOKEN_INVALID` | Obtain a new challenge and a new Google ID token. Check Web client ID, certificate registration, and device time if repeated. |
| 401 | `GOOGLE_CHALLENGE_INVALID` | Challenge expired, nonce mismatched, or was used; start a fresh attempt. |
| 409 | `ACCOUNT_LINK_REQUIRED` | Tell the passenger to sign in to the existing email/password account. Do not create a second account or auto-link. |
| 503 | `GOOGLE_NOT_CONFIGURED` | Server needs `GOOGLE_WEB_CLIENT_ID`; show Google sign-in temporarily unavailable. |
| 401 | `INVALID_REFRESH_TOKEN` | Clear local Rida session and require sign-in. |
| 503 | `AUTH_TEMPORARILY_UNAVAILABLE` | Retain saved credentials; retry later. |
| 429 | Rate limited | Respect `Retry-After`; do not hammer sign-in. |

Credential Manager cancellation means the user dismissed Google UI; keep them on the login screen. Do not classify it as an invalid account. If no Google account is available on the phone, offer normal email/password login or let the passenger add a Google account to the device.

## Integration acceptance checks

- With an unregistered Google account, the exchange returns a Rida `user.id`, email, optional full name, and tokens. `/api/v1/me` returns that same Rida ID and saved email/name after app restart and refresh.
- Signing in with the same Google account again resolves to the **same Rida user ID**. A later Profile name edit survives another Google sign-in.
- A Google token without `name` still signs in; Profile displays "Add your full name". No placeholder personal data is saved.
- A new Google user has no fabricated phone. Profile completion and phone verification remain independent of authentication.
- A second use of the same challenge fails. An expired or incorrect nonce fails. Token and challenge failures never create a Rida user.
- An existing password account with the same email returns `ACCOUNT_LINK_REQUIRED` and remains intact.
- Refresh works after app restart without a Google prompt; logout revokes the current Rida session.

Backend code references: `controllers/mobile_auth_controller.go`, `utils/google_id_token.go`, `store/google_challenge.go`, `store/oauth.go`, and `controllers/auth_controller.go`. The full API contract is in the repository's [mobile auth guide](https://github.com/Ditronics-Tz/RideBackend-Rewrite-/blob/main/docs/api/mobile-auth.md) and [OpenAPI document](https://github.com/Ditronics-Tz/RideBackend-Rewrite-/blob/main/docs/api/openapi.yaml).

## Official references

- [Implement Sign in with Google using Credential Manager](https://developer.android.com/identity/sign-in/credential-manager-siwg-implementation)
- [AndroidX Credential Manager releases](https://developer.android.com/jetpack/androidx/releases/credentials)
- [Google ID SDK release notes](https://developers.google.com/identity/android-credential-manager/releases)
- [Android OAuth client signing certificates](https://developers.google.com/android/guides/client-auth)
- [Google OpenID Connect ID token claims](https://developers.google.com/identity/openid-connect/reference)
