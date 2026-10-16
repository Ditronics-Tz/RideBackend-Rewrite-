# Rida Android fix: promo validation and notification updates

**For:** passenger Android app `com.example.ridepassenger2` (Kotlin, Compose, Retrofit, OkHttp, Moshi)  
**API base:** `https://rida-backend.ditronics.co.tz/api/v1/`  
**Backend:** fix `2eb0db6` is on GitHub `main` and was deployed on 16 October 2026. This file describes the mobile changes still required. Firebase/FCM is not used in this flow.

## 1. Why `TEST` returned HTTP 400

At the time of investigation, `TEST` was active, selected for all three active passengers, and required a **minimum fare of TZS 1,000**. The tested passenger had an audience grant and no prior completed ride or promo redemption blocking the code. Sharing a promo with passengers creates inbox messages and audience grants; it does not apply a discount to a ride.

The reported 400 response was 173 bytes. With the backend's request ID included, that size matches this exact validation error:

```json
{
  "error": "fare must be a non-negative whole TZS amount",
  "fields": { "fare": "fare must be a non-negative whole TZS amount" },
  "request_id": "<request ID>"
}
```

The Android request therefore sent `fare` as missing, `null`, or a negative number. The captured log does not distinguish those three cases. The `PromotionDetailsScreen` call site uses a nullable `Long`, so check where its fare comes from.

### Mobile change

`POST /promos/validate` requires an actual quoted fare in **whole TZS**. Use a non-null `Long`; do not send `null`, `-1`, or omit the field. Example request, assuming the actual quote is TZS 1,000:

```json
{"code":"TEST","fare":1000}
```

```kotlin
data class ValidatePromoRequest(val code: String, val fare: Long)
data class PromoSummary(val code: String, val title: String)
data class ValidatePromoResponse(
    val valid: Boolean,
    @Json(name = "discount_amount") val discountAmount: Long,
    @Json(name = "final_fare") val finalFare: Long,
    val promo: PromoSummary
)

interface PromoApi {
    @POST("promos/validate")
    suspend fun validate(@Body request: ValidatePromoRequest): ValidatePromoResponse
}

// In the ViewModel/action handler, after a ride quote exists:
val fare = quotedFareTzs
if (fare == null || fare < 0L) {
    // Show "Choose a ride to check this discount" and wait for a quote.
} else {
    val result = promoApi.validate(ValidatePromoRequest(code.trim(), fare))
    // Display result.discountAmount and result.finalFare.
}
```

Keep the promo details page visible when no fare exists; disable only the fare-dependent validation action. Read the JSON error body for 400 and show `fields.fare`, `fields.code`, or `error`. A valid fare can still return 400 if the promo has expired, its minimum fare is not met, its limit is reached, or the passenger is ineligible. The backend enforces those rules during booking again.

## 2. Why notification updates returned HTTP 500

The backend previously used the same 20-second context for both waiting and its final database query. When the query ran as the context expired, the timeout error became HTTP 500. The server log for the reported call showed `status=500` after **20,008 ms**. The deployed fix now uses an 18-second wait with separately bounded database checks.

### Mobile change

Keep the authenticated long-poll request open long enough. Configure the OkHttp read timeout to at least **30 seconds** for this API client. A normal idle response is HTTP 200:

```json
{"changed":false,"latest_id":"2500d131-71ad-4c26-9abe-de2e5f672869"}
```

On `changed=true`, fetch the full inbox using `GET /passenger/notifications`. Use the newest `id` from that response as the next `after` value. Keep the old `after` if fetching the inbox fails, so a missed notification is picked up on retry. If the inbox was empty, omit `after`.

```kotlin
data class InboxUpdate(
    val changed: Boolean,
    @Json(name = "latest_id") val latestId: String
)

interface NotificationApi {
    @GET("passenger/notifications/updates")
    suspend fun updates(@Query("after") after: String?): InboxUpdate
}
```

Run the update loop only while the signed-in app is foregrounded. Reconnect after every 200 response. Cancel it on logout/background; fetch the inbox immediately on foreground resume. On network errors or HTTP 503, retry with bounded backoff **without logging the passenger out**. On HTTP 401, follow the app's existing access-token refresh flow. On HTTP 429, honor `Retry-After`.

This endpoint signals changes to the durable inbox; it does not deliver a guaranteed immediate Android system alert while the app is closed. Background WorkManager sync may be delayed by Android.

## 3. Quick verification after the mobile change

1. With a real ride quote of at least TZS 1,000, validate `TEST` as a passenger eligible for its audience. Confirm the JSON request contains a numeric `fare`.
2. If promo validation still returns 400, capture the **redacted response JSON** (`error`, `fields`, `request_id`) and the non-secret fare value; never log bearer or refresh tokens.
3. Open the notifications screen, fetch the inbox, then call `/passenger/notifications/updates?after=<newest-id>`. With no new message it should return 200 and `changed:false` after roughly 18 seconds.
4. Share a new promo from admin while the app is open. The next update should return `changed:true`; fetch the inbox to display the new item.

The backend build, VPS image build, Compose configuration, and public readiness check passed after deployment. An authenticated handset request after this fix was not available during deployment, so the mobile steps above remain to be verified on a device.
