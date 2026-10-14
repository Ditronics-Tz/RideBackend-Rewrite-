# Mobile app changes

## Implemented backend contracts (B3/B4/B5/B6/B7/B8/mobile passenger)

- Driver: when R2 is enabled, request document presign with type/content_type/size/
  expires_at, PUT bytes to the returned URL with its headers, then confirm by ID.
  Request a new upload after the five-minute deadline. Legacy multipart remains
  available only for local development storage.
- Both apps: load protected documents with authenticated metadata requests and
  short-lived storage URLs. Fetch fresh metadata on expiry. Do not persist signed
  URLs or send app credentials to object storage. Profile image URLs resolve
  through the API to the approved public custom domain.
- Both apps: pageable list endpoints default to 50 rows, maximum 200; follow
  total/limit/offset. Errors include request_id for support correlation; clients
  may send a safe X-Request-ID. Invalid pagination returns field errors.
- App auth returns `access_token`, the compatibility `token` alias, and a refresh
  token. Keep refresh tokens in secure platform storage; call
  `POST /api/v1/auth/refresh` before access expiry and replace both values after
  every successful refresh. Reuse invalidates that session family. Logout and
  logout-all revoke server sessions; `restricted=true` means pending-deletion
  recovery routes only.
- Cross-user driver/passenger profile lookups now return public DTOs. Driver
  fields are name, vehicle type, image and rating; passenger fields are name,
  image and rating. Contact and compliance identifiers remain available to the
  profile owner, with full staff review behind the audited admin profile route.
- Profile image uploads accept JPEG/PNG/WebP bytes and return a PNG-backed
  profile image URL. The account quota is three valid uploads per hour; clients
  should display the 429 Retry-After value and let users retry later.
- Passengers create rides with pickup/dropoff points and vehicle type. The
  server returns the estimated fare, status, current driver (when offered), and
  event timeline. Send a unique `Idempotency-Key` on create, cancel, driver
  offer actions, lifecycle transitions, availability changes and location
  updates. Do not send a final fare; the server computes it at completion.
- Drivers must set availability with `PUT /api/v1/driver/availability`, publish
  fresh coordinates with `POST /api/v1/driver/location`, poll
  `GET /api/v1/driver/offers`, and use the accept/decline/arrived/start/complete
  actions. Ride reads are restricted to participants. Configure fare rates on
  the backend before enabling ride creation; no payment, rating, promo or
  notification side effects run on completion yet.
- Both apps can open support tickets with `POST /api/v1/support/tickets` and
  list only their own tickets. After a ride completes, each ride participant
  may rate the other once at `POST /api/v1/rides/:id/rating`. Existing public
  rating lists omit ratings hidden by an admin.

## Passenger integration additions (migration version 9)

- Apply migration 9 before starting this backend build. The API refuses to start
  against an older schema. `GET/POST /api/v1/passenger/saved-places` and
  `PUT/DELETE /api/v1/passenger/saved-places/:id` persist up to 20 geocoded
  places per passenger. Home and Work each allow one entry; custom entries may
  repeat. Send `kind`, `label`, `address`, `lat`, and `lng`. The Android app must
  resolve coordinates before saving a place.
- `GET/PATCH /api/v1/passenger/preferences` persists ride, communication, and
  language toggles. PATCH accepts the changed fields, including explicit
  `false`. `language` is `en` or `sw`. These are stored preferences; external
  email/SMS/push delivery and application locale switching still need mobile
  and provider integration.
- `POST /api/v1/rides/quote` accepts the same pickup/dropoff/vehicle payload as
  ride creation and returns `estimated_fare_tzs`, `currency: TZS`, straight-line
  distance, estimated duration, and `payment_method: cash`. Show the server fare
  in the booking UI. The app's OSRM road distance/ETA may differ from the
  server's straight-line estimate. A quote does not hold a driver or fare.
- `GET /api/v1/rides/:id/driver-location` returns the assigned driver's latest
  point only for an active ride participant. Poll while the ride is active;
  `204` means clear the driver pin. Points older than two minutes and ended
  rides return `204`; another user receives `404`.
- `POST /api/v1/scheduled-rides` requires an `Idempotency-Key` and the same
  pickup/dropoff/vehicle fields plus RFC3339 `scheduled_for` (5 minutes to 30
  days ahead). `GET /scheduled-rides?status=upcoming` and
  `GET /scheduled-rides/:id` expose the
  persisted status; `POST /scheduled-rides/:id/cancel` works before dispatch.
  Fare rates must be configured before scheduling. The backend worker attempts
  normal dispatch when due. `dispatched` includes a
  `ride_id`; `failed` can include `no_driver_found` and a ride ID for the
  ordinary ride detail. A schedule is a dispatch attempt, not a driver guarantee.
- `GET /api/v1/support/tickets/:id` returns ticket, paged public messages, and
  attachment metadata. `POST /support/tickets/:id/messages` accepts `{body}`;
  retries require the same `Idempotency-Key`. `POST /attachments` accepts a
  multipart `file` (JPEG, PNG, or PDF, at most 5 MiB), also with an idempotency
  key. Download through the authenticated
  `GET /support/tickets/:id/attachments/:attachmentId`. Staff have equivalent
  `/admin/tickets/:id/messages` and `/admin/tickets/:id/attachments` routes.
  Internal staff notes never appear in the app conversation.

Cash remains the only ride payment method. Wallet top-ups and provider methods
still reject requests until a local payment provider is integrated. Static help
and legal copy can stay in the app; parcel deliveries require a separate product
contract before they can be represented as real courier orders. No mobile source
code was changed in this backend run. Use the generated OpenAPI contract while
integrating the passenger and driver apps.
