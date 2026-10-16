# Rida passenger app: promo codes and notifications without Firebase

Audience: Android engineer working on `com.example.ridepassenger2` (Kotlin, Compose, Retrofit, OkHttp, Moshi). API base: `https://rida-backend.ditronics.co.tz/api/v1`. Use `Authorization: Bearer <accessToken>` and the existing `/auth/refresh` contract.

## What is implemented

The backend stores one durable inbox item per passenger for promo shares and supported ride/account events. Admin creates or activates a promo and then selects **Share with audience**; saving alone does not notify users. The Android app must fetch and display its inbox. A new long-poll endpoint tells an open app when the inbox has changed. Firebase and an FCM device token are not part of this flow. Google Sign-In is independent and remains unchanged.

## Promo API (passenger)

All paths below are relative to `/api/v1`; all require a passenger Bearer token.

### `GET /promos/available?fare=10000`

`fare` is a nonnegative integer in whole TZS. Returns `{"promos":[{"id":"UUID","code":"RIDA20","title":"Weekend ride","description":"Save on your next ride","discount_type":"percent","discount_value":20,"max_discount":3000,"min_fare":5000,"audience":"all","first_ride_only":false,"starts_at":"2026-10-16T00:00:00Z","expires_at":"2026-11-16T00:00:00Z","max_redemptions":100,"max_per_user":1,"redeemed_count":0,"status":"active","effective_status":"active","discount_amount":2000,"final_fare":8000}],"total":1,"limit":50,"offset":0}`. Some fields are nullable, including `max_discount` and `max_redemptions`. A returned discount is a preview; fare and eligibility are checked again during booking.

### `POST /promos/validate`

Request: `{"code":"RIDA20","fare":10000}`. **`fare` is required** as a non-negative integer in whole TZS; pass the actual ride quote, not null or a placeholder. If the promo details screen does not yet have a quote, show the promo details and wait until a quote exists before calling validate. A promo may also require a minimum fare. Success: `{"valid":true,"discount_amount":2000,"final_fare":8000,"promo":{"code":"RIDA20","title":"Weekend ride"}}`. The existing rate limit is ten checks per minute per app user. Invalid or unavailable promo returns a 4xx structured promo error with `error` and `fields`; show that message in the UI. A missing or negative fare returns `fields.fare = "fare must be a non-negative whole TZS amount"`. 429 includes `Retry-After`. Do not assume validation reserves a promo. The ride booking API performs the actual reservation/redemption.

For Kotlin, model the request as `data class PromoValidationRequest(val code: String, val fare: Long)` and call validate only when the quoted fare is available. Do not convert a nullable fare to `-1`, `null`, or a missing JSON field. Decode the 400 response body and show `fields.fare` or `error` to the passenger.

### `GET /referral/me` and `POST /referral/apply`

Existing referral endpoints are unchanged. Request for apply: `{"code":"FRIEND123"}`. Keep referral codes separate from promo codes in the UI.

## Durable notification inbox API

All paths are relative to `/api/v1`, require a passenger Bearer token and return `Cache-Control: private, no-store`.

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

`kind` is `promo`, `ride`, or `system`; allow unknown future values. `data` is a string map. Ride messages include `ride_id` and `status`. `read_at` is an ISO-8601 UTC timestamp or null. Deduplicate by `id`. Fetch on app start, login, foreground resume and opening the inbox screen. Paginate with `limit`/`offset`.

### `GET /passenger/notifications/updates?after=<latest-id>`

New foreground long-poll endpoint. Pass the `id` of the first (newest) item in the last inbox response. If the inbox was empty, omit `after`. The server checks committed notifications for this passenger only. It returns as soon as a new latest item exists or after about 18 seconds. Examples:

```json
{"changed":true,"latest_id":"8f8d5cb3-63f3-4a8c-9ed7-4d5c442039a1"}
```

```json
{"changed":false,"latest_id":"8f8d5cb3-63f3-4a8c-9ed7-4d5c442039a1"}
```

`changed=true` is a signal to fetch `GET /passenger/notifications`; it does not contain notification text. After each response, open another update request while the app is foregrounded. Keep the previous `after` until the inbox fetch succeeds, then replace it with the newest returned ID; this prevents losing messages after a failed fetch. If the inbox remains empty, omit `after` on the next request. Invalid `after` returns 400; revoked session returns 401; temporary authentication/database errors return 503; 429 means slow down. Use an OkHttp read timeout longer than 21 seconds (for example 30 seconds), cancel the request when the app goes to background, and retry network/5xx errors with bounded backoff. Do not clear login state on transient errors. Refresh an expired access token using the existing refresh flow.

### `POST /passenger/notifications/{id}/read`

No body. Returns 204. Invalid ID returns 400; another passenger's or missing ID returns 404.

### `POST /passenger/notifications/read-all`

No body. Returns 204. Marks current inbox items read.

## Mobile implementation

In Compose, provide an unread badge, paginated inbox, empty/error states, and navigation for known `promo_id` or `ride_id`. Never follow arbitrary strings in `data` as URLs. Show the item from the Rida API rather than treating `latest_id` as message content. Optionally show a local Android notification while the app is open, after checking user preferences and Android 13+ notification permission. This local alert is generated by the app when its live request detects a new inbox item.

```kotlin
data class InboxUpdate(
  val changed: Boolean,
  @Json(name = "latest_id") val latestId: String
)

interface RidaNotificationApi {
  @GET("passenger/notifications")
  suspend fun inbox(@Query("limit") limit: Int = 20, @Query("offset") offset: Int = 0): InboxPage

  @GET("passenger/notifications/updates")
  suspend fun updates(@Query("after") after: String?): InboxUpdate

  @POST("passenger/notifications/{id}/read")
  suspend fun markRead(@Path("id") id: String): Response<Unit>

  @POST("passenger/notifications/read-all")
  suspend fun markAllRead(): Response<Unit>
}
```

`InboxPage` contains `notifications: List<InboxItem>`, `total: Long`, `unread_count: Long`, `limit: Int`, `offset: Int`. `InboxItem` contains `id`, `kind`, `title`, `body`, `data: Map<String,String>`, nullable `read_at`, and `created_at`. Use Moshi `@Json` for snake_case names. Run the update loop in an app-level coroutine tied to authenticated foreground lifecycle, not in a single screen. Cancel it on logout or background; refetch the inbox when foreground resumes.

If background discovery is acceptable, use periodic WorkManager sync with a minimum 15-minute interval; Android may delay it for battery management. This cannot promise immediate phone alerts while the app is closed. [Android WorkManager documentation](https://developer.android.com/develop/background-work/background-tasks/persistent/getting-started/define-work). A standard Android app cannot maintain a guaranteed background connection for instant delivery without a system-supported push transport; Android also restricts starting foreground services from the background. [Android background service restrictions](https://developer.android.com/develop/background-work/services/fgs/restrictions-bg-start).

## Notification preferences

`GET /passenger/preferences` and `PATCH /passenger/preferences` remain available. Example patch: `{"promotions":true,"ride_updates":true,"language":"sw"}`. Use these preferences when deciding whether to show an optional local alert. The inbox remains the durable source of messages. `push_enabled` is a legacy setting and has no effect on this Firebase-free flow.

## Backend deployment

Migration `00014_app_notifications.sql` is already required for the inbox. No new migration or Firebase credential is needed for the update endpoint. Keep `PUSH_PROVIDER=none`; use the base `docker-compose.yml` only. Deploy backend changes using the existing migration/build/restart process in `DOCKER_DEPLOYMENT.md`. Existing Google Sign-In, sessions, promos, and stored inbox messages are unaffected. The Android app source is a separate project and must implement the API calls above before users see these notifications.
