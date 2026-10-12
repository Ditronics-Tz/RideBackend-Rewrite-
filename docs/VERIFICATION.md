# Verification and support accounts — step 1

## Policy and state

Driver verification uses `pending_documents → submitted → under_review → approved/rejected`, with `expired` and the legacy verification restriction `suspended`. Required uploads are `driving_licence`, `vehicle_card`, `national_id`, `vehicle_photo`. `insurance` is accepted but optional, following the latest product decision. Licence and insurance uploads require expiry dates. Date-only expiry is valid through that date in Africa/Dar_es_Salaam; internally the boundary is the following midnight.

Uploading a replacement archives the previous document row and retains its private file and review history. Unchanged approved documents stay approved. Submit only after all required documents and licence/card/plate identifiers are present. Claim before reviewing. Each rejection requires a reason code and note. Reviewing all documents does **not** grant final approval: the claiming reviewer explicitly approves the driver. Support cannot alter an existing document decision; an admin can override/revoke with a reason after releasing any other reviewer's claim.

Account `status` is independently `active`, `suspended`, or `banned`. `is_active` stays synchronized for compatibility. Suspension blocks login, OAuth login, every production `AppProtected` route and private file access. Optional `until` automatically restores account access on its next authenticated request. It preserves document decisions and never takes a driver online automatically. Only admins can ban or lift a ban. The driver availability gate also requires current, unexpired approved documents. `RequireDriverApproval` is supplied for a future dispatch implementation; **there is no ride acceptance/assignment endpoint in this repository to integrate yet**. Call this gate within the future ride assignment transaction and retain account/document locking.

## API

All routes start with `/api/v1`. Driver routes use an app JWT; staff routes use a staff JWT and existing password/2FA requirements.

| Method and path | Caller | Body / result |
| --- | --- | --- |
| `POST /driver/documents` | Driver | Multipart `type`, `file`, optional `expires_at`; 201 document |
| `PUT /driver/profile/me` | Driver | `licence_number`, `vehicle_card_number`, `plate_number` |
| `GET /driver/verification/me` | Driver | Profile, current documents, immutable reviews, policy |
| `POST /driver/verification/me/submit` | Driver | No body |
| `PATCH /driver/availability` | Driver | `{ "online": true/false }` |
| `GET /driver/notifications` | Driver | Persisted expiry notifications |
| `GET /files/documents/:id` | Owner, staff | Private JPEG/PNG/WebP/PDF, including archived versions |
| `GET /admin/verifications?status=` | Support, admin | Defaults to submitted + under_review |
| `POST /admin/verifications/:driverId/claim` | Support, admin | No body; atomically claims driver |
| `PATCH /admin/documents/:id/review` | Support, admin | `decision` approved/rejected, `reason_code`, `note` |
| `POST /admin/drivers/:id/approve` | Claim owner | No body; validates all current uploads |
| `POST /admin/users/:id/suspend` | Support, admin | Required `reason`, optional RFC3339 `until` |
| `POST /admin/users/:id/reactivate` | Support, admin | Required `reason` |
| `POST /admin/users/:id/ban` | Admin | Required `reason` |
| `GET/POST /admin/users/:id/notes` | Support, admin | GET notes; POST `{ "body": "..." }` |
| `DELETE /admin/users/:id/notes/:noteId` | Admin | Deletes a note, audited |
| `GET /admin/users/:id/history` | Support, admin | `status_events`, `verification_reviews` |
| `GET /admin/users?role=&q=` | Support, admin | Lookup name/email/phone/plate |
| `POST /auth/phone/request-otp` | App user | `{ "phone": "0712345678" }` |
| `POST /auth/phone/verify-otp` | App user | `{ "code": "123456" }` |

The earlier `/admin/drivers/:id/verification` GET/claim/release and POST document-review routes remain compatible. `/admin/drivers/:id/suspension` now controls account suspension. Boolean `/verify` returns 410. Old licence uploads return 410; profile pictures remain supported. `/files/licences/:driverId` remains owner/staff only for legacy clients.

Rejection codes: `blurry_image`, `expired`, `name_mismatch`, `number_mismatch`, `wrong_document`, `incomplete_document`, `suspected_fraud`, `other`. Invalid input returns 400; conflicting claim/state/duplicate returns 409; staff authorization failures return 403.

Files are content-sniffed, limited to 8 MB, and stored outside public uploads. Vehicle photos must be images. Claim, review and expiry changes serialize on the driver row. Licence/plate numbers have partial unique indexes and duplicate attempts create staff-visible flags. Other account identifiers are omitted from driver-facing responses. Every authenticated admin-route request is audited, including support reads; staff private-document access is audited separately. Decision history is separate from the staff audit log and available to the driver.

## Phone OTP

Phone numbers live on users, normalized to `+255…`, and are unique when nonempty. Verification timestamps are set only after successful OTP verification; the legacy passenger profile phone mirrors the verified value. Direct passenger profile phone changes are refused. Passengers need no signup documents.

Implement `store.SMSSender.SendOTP(ctx, phone, code)` and set `appStore.SMS` before building the app. No provider or credentials are assumed; requests return 503 until configured. Email is not used to prove phone ownership. OTP codes are random, HMAC-protected at rest, never returned/logged, expire after five minutes, allow five attempts, enforce a 60-second resend delay, and cannot be reused. Unique phone ownership is checked again at verification by the database.

## Migration and operation

Restart the backend normally; startup auto-migrates new tables and columns. Back up existing deployment data first. Legacy licence files are imported once as submitted documents. Existing boolean approvals are not trusted as approval of newly required documents; legacy drivers must complete and pass review. The old database `verified` column is retained but no longer used or exposed. Existing disabled accounts migrate to suspended. Legacy passenger phones migrate as **unverified**. Invalid or duplicate legacy phone/licence/plate values stop the migration with an actionable error; accounts are never silently merged or deleted.

The expiry worker catches up at startup and runs each midnight EAT. It marks expired documents, takes affected drivers offline, preserves decisions, and inserts one persistent notification per expired document. Availability and verification reads also catch missed expiry sweeps. Notifications are in-app API records; SMS/push delivery is not configured. A running backend is required for the scheduled job.

The admin frontend supports document review, final approval, admin overrides, private previews, duplicate warnings, driver/passenger account controls, optional suspension dates, notes and histories. Passenger details and driver Account Activity contain the shared support panel.

Step 1 does not implement ticket conversations/team assignment, actual ride timelines/cancellation/refunds, user export, or abuse-triggered passenger ID collection. Tickets/complaints/rides in the old backend are still placeholders; automatic complaint thresholds need persisted complaints first. Low-rating thresholds and passenger ID adjudication need a separate explicit policy rather than collecting IDs at signup.

## Verification commands

`go vet ./...`; `go test -p 1 ./...`; and `RUN_POSTGRES_VERIFICATION_TESTS=1 go test ./store -run TestPostgresConcurrentVerification -count=1` (PowerShell: set `$env:RUN_POSTGRES_VERIFICATION_TESTS='1'`). The PostgreSQL test creates and drops its own UUID-named schema and never migrates the application schema. Frontend: `npm test`, `npm run typecheck`, `npm run lint`, `npm run build`.
