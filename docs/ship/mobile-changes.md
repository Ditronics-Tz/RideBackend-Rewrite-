# Mobile app changes

## Implemented backend contracts (B3/B4/B5/B6)

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

## Future contracts (not implemented in this milestone)

- B6: public profile DTOs and upload safeguards.
- B7/B8: server-authoritative ride creation, offers, transitions, availability,
  location, participant reads and ride-bound ratings. Mobile clients must adopt
  those endpoints after they are implemented; no new dispatch flow is live yet.

No mobile source code was changed in this run. Consult api-changes.md and the
current OpenAPI inventory before integrating a deployed backend.
