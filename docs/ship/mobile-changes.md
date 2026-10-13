# Mobile app changes

## Implemented backend contracts (B3/B4)

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

## Future contracts (not implemented in this milestone)

- B5: fifteen-minute access tokens, rotating hashed refresh sessions, refresh,
  logout/logout-all and session management. Existing token alias compatibility
  must be retained when this lands. Do not assume the current app auth changed.
- B7/B8: server-authoritative ride creation, offers, transitions, availability,
  location, participant reads and ride-bound ratings. Mobile clients must adopt
  those endpoints after they are implemented; no new dispatch flow is live yet.

No mobile source code was changed in this run. Consult api-changes.md and the
current OpenAPI inventory before integrating a deployed backend.
