# Rida passenger app: promo codes and notifications

Audience: Android engineer working on `com.example.ridepassenger2` (Kotlin, Compose, Retrofit, OkHttp, Moshi). API base: `https://rida-backend.ditronics.co.tz/api/v1`. Use the app access token as `Authorization: Bearer <accessToken>`; refresh with the existing `/auth/refresh` contract. Do not put any Google or Firebase server credential in the Android app.

## What the backend implements

Promos already had creation, audience, validity, validation, redemption and admin management. This change adds a durable passenger inbox, connects promo sharing and ride/account events to it, and adds optional FCM delivery. The inbox is the source of truth. FCM only signals a new message and is **not** proof that Android displayed it. Existing mobile clients need no changes to keep using their current API; to display these new notifications, the passenger app must implement the inbox UI and sync described below.

The admin frontend already has **Promotions → Create promo** at `/promotions/new`. Admin can save a draft, activate it, choose all or selected passengers, then open the promo detail and press **Share with audience**. Saving or activating alone does not send notifications. Sharing persists one inbox item per selected passenger, including a promo ID and code. Sharing again creates another item. The frontend now previews this message and shows backend push status in **Notification deliveries**. Support may view promos; only admin may create, edit or share.

## Promo API (passenger)

All paths below are relative to `/api/v1`; all require a passenger Bearer token.

### `GET /promos/available?fare=10000`

`fare` is a nonnegative integer in whole TZS. Returns `{"promos":[{"id":"UUID","code":"RIDA20","title":"Weekend ride","description":"Save on your next ride","discount_type":"percent","discount_value":20,"max_discount":3000,"min_fare":5000,"audience":"all","first_ride_only":false,"starts_at":"2026-10-16T00:00:00Z","expires_at":"2026-11-16T00:00:00Z","max_redemptions":100,"max_per_user":1,"redeemed_count":0,"status":"active","effective_status":"active","discount_amount":2000,"final_fare":8000}],"total":1,"limit":50,"offset":0}`. Some fields are nullable, including `max_discount` and `max_redemptions`. A returned discount is a preview; fare and eligibility are checked again during booking.

### `POST /promos/validate`

Request: `{"code":"RIDA20","fare":10000}`. Success: `{"valid":true,"discount_amount":2000,"final_fare":8000,"promo":{"code":"RIDA20","title":"Weekend ride"}}`. The existing rate limit is ten checks per minute per app user. Invalid or unavailable promo returns a 4xx structured promo error; 429 includes `Retry-After`. Do not assume validation reserves a promo. The ride booking API performs the actual reservation/redemption.

### `GET /referral/me` and `POST /referral/apply`

Existing referral endpoints are unchanged. Request for apply: `{"code":"FRIEND123"}`. Keep referral codes separate from promo codes in the UI.

## Durable notification inbox API

All paths require a passenger Bearer token and return `Cache-Control: private, no-store`.

### `GET /passenger/notifications?limit=20&offset=0`

Success (`200`):

```json
{
  "notifications": [
    {
      "id": "8f8d5cb3-63f3-4a8c-9ed7-4d5c442039a1",
      "kind": "promo",
      "title": "Weekend ride",
      "body": "Use code RIDA20 for your next eligible ride.",
      "data": { "promo_id": "e84763cf-546b-4239-9b1c-459c7b51debd", "code": "RIDA20" },
      "read_at": null,
      "created_at": "2026-10-16T12:00:00Z"
    }
  ],
  "total": 1,
  "unread_count": 1,
  "limit": 20,
  "offset": 0
}
```

`kind` is currently `promo`, `ride`, or `system`. `data` is a string map. For `ride`, it contains `ride_id` and `status` (`assigned`, `driver_arrived`, `in_progress`, `completed`, `cancelled_by_driver`, or `cancelled_by_admin`). For supported account/finance/safety events, `system` contains `template`. `read_at` is ISO-8601 UTC or JSON null. Treat unknown kinds/keys as a generic message. Deduplicate by `id`. Fetch new pages on app start, after login/refresh, when the notifications screen opens, and on foreground resume. Use `total`/`offset` for pagination.

### `POST /passenger/notifications/{id}/read`

No body. Returns `204`. Repeating it is safe. Invalid ID returns `400`; another user's/missing ID returns `404`.

### `POST /passenger/notifications/read-all`

No body. Returns `204`. Mark all current items read.

Recommended Compose screens/components: unread badge, paginated inbox list, empty/error states, item detail/tap action, promo CTA to the promo/booking screen, ride CTA to the ride details screen, and settings links for promotions/push. Never trust `data` URLs or arbitrary navigation strings; navigate only on known kind and valid UUID. Show inbox content even if FCM is unavailable.

## Notification preferences

`GET /passenger/preferences` and `PATCH /passenger/preferences` already exist. Example patch: `{"promotions":true,"ride_updates":true,"push_enabled":true,"language":"sw"}`. `promotions`, `ride_updates`, and `push_enabled` govern **push delivery**. The durable inbox keeps messages so a passenger can view important history after a push failure or provider change. Default `promotions` may be false until the user enables it, so provide a clear opt-in in Settings. Do not claim a phone alert is guaranteed.

## Optional FCM integration

Firebase is **not required** for the promo system, inbox, or booking. Without FCM, call the inbox API on app start/foreground and optionally schedule WorkManager periodic sync. Android WorkManager periodic tasks have a [15-minute minimum interval](https://developer.android.com/develop/background-work/background-tasks/persistent/getting-started/define-work) and the OS may delay execution, so this route cannot promise instant background alerts. FCM provides the familiar background notification experience on compatible Android devices. Another push transport can be added later behind the backend `PushSender` interface.

If the product chooses FCM:

1. Create or use a Firebase project, register the exact Android package `com.example.ridepassenger2`, and add the Android Firebase config to the mobile project. Enable Cloud Messaging HTTP v1. Follow the [official Android setup](https://firebase.google.com/docs/cloud-messaging/android/get-started). This mobile config is not the server service-account key.
2. Request `POST_NOTIFICATIONS` permission on Android 13+, and provide a `FirebaseMessagingService` for foreground notifications and token refresh. Follow [Android receive guidance](https://firebase.google.com/docs/cloud-messaging/android/receive-messages).
3. After authenticated login and whenever the FCM registration token changes, call `POST /passenger/push-devices` with `{"token":"<FCM registration token>"}`. Success (`201`): `{"id":"UUID","platform":"android","created_at":"2026-10-16T12:00:00Z"}`. Persist the returned device ID locally per signed-in account. The server encrypts the token at rest. A token already attached to another account returns `409`; sign out/unregister from the old account first. Missing backend FCM encryption configuration returns `503`; the inbox still works.
4. `GET /passenger/push-devices` returns `{"devices":[{"id":"UUID","platform":"android","created_at":"..."}]}` without tokens. `DELETE /passenger/push-devices/{id}` returns `204`. On logout, call DELETE when practical, then the existing `/auth/logout`; backend logout and logout-all revoke bound push devices automatically. On token refresh, call POST again and remove obsolete device IDs when known.
5. The FCM payload has `notification.title`, `notification.body`, and data keys `notification_id`, `kind`, optionally `promo_id` or `ride_id`. On tap/foreground, fetch the inbox item or full inbox from Rida API before navigation. Do not put auth tokens in FCM data. A `sent` delivery status means FCM accepted the request, not delivery/display on the handset.

## Retrofit/Moshi sketch

```kotlin
data class InboxPage(
  val notifications: List<InboxItem>,
  val total: Long,
  @Json(name = "unread_count") val unreadCount: Long,
  val limit: Int,
  val offset: Int
)
data class InboxItem(
  val id: String,
  val kind: String,
  val title: String,
  val body: String,
  val data: Map<String, String>,
  @Json(name = "read_at") val readAt: String?,
  @Json(name = "created_at") val createdAt: String
)
data class PushDeviceRequest(val token: String)

interface RidaNotificationApi {
  @GET("passenger/notifications")
  suspend fun inbox(@Query("limit") limit: Int = 20, @Query("offset") offset: Int = 0): InboxPage
  @POST("passenger/notifications/{id}/read")
  suspend fun markRead(@Path("id") id: String): Response<Unit>
  @POST("passenger/notifications/read-all")
  suspend fun markAllRead(): Response<Unit>
  @POST("passenger/push-devices")
  suspend fun registerDevice(@Body request: PushDeviceRequest): PushDeviceResponse
  @DELETE("passenger/push-devices/{id}")
  suspend fun unregisterDevice(@Path("id") id: String): Response<Unit>
}
```

Use the existing authorized Retrofit client and one refresh interceptor. On transient 5xx/network errors keep the session, retain cached inbox rows, and retry later. On 401 follow the existing app session refresh policy; do not log out on a transient failure.

## Backend deployment

Migration `00014_app_notifications.sql` is required before starting the new API. It creates inbox, encrypted push-device and push-delivery tables; it does not modify existing users or promos. Run the existing migration command, then recreate API as described in `DOCKER_DEPLOYMENT.md`. `PUSH_PROVIDER=none` is the default. This requires no Firebase account or credential and makes the inbox available immediately.

To enable FCM later, set `FIREBASE_PROJECT_ID`, generate a stable `PUSH_TOKEN_ENCRYPTION_KEY` (`openssl rand -hex 32`), put a real service-account JSON at `secrets/firebase-service-account.json` with restrictive permissions, then use the optional overlay:

```bash
docker compose -f docker-compose.yml -f docker-compose.fcm.yml config -q
docker compose -f docker-compose.yml -f docker-compose.fcm.yml up -d --build api
```

The JSON and `.env` stay outside Git and the Docker build context. Do not reuse the Google Sign-In Web client secret; it is not an FCM server credential. The app starts with FCM only when Application Default Credentials can be loaded. Keep the encryption key stable; changing it prevents decryption of already registered device tokens, requiring app re-registration. The backend retries transient FCM failures with backoff; invalid registration tokens are deactivated. Multiple push deliveries can occur after worker crashes because FCM send and database update are separate operations; Android should deduplicate by `notification_id`.
