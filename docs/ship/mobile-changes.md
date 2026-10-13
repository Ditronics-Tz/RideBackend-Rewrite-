# Mobile app changes

## Implemented backend contracts (B3/B4/B5/B6/B7)

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

## Future contracts (not implemented in this milestone)

- B8: ride-bound ratings and operational support/admin workflows.

No mobile source code was changed in this run. Consult api-changes.md and the
current OpenAPI inventory before integrating a deployed backend.
