# Firebase push for passenger and driver apps

Updated 2026-11-08. Code is wired for FCM in both Android apps and the backend. Backend tests, vet and generated API-contract checks pass. Server activation is a separate operational step; handset display requires updated apps, authenticated device registration and notification permission. Never infer real handset delivery from source checks or provider acceptance.

## What the code does

- Both apps include Firebase Messaging, a non-exported `FirebaseMessagingService`, an Android notification channel/icon, and Android 13+ notification permission requested after login. Analytics alone is not a push transport.
- The Application observes login changes. It obtains the Firebase device token and registers it with an explicit captured bearer token at `POST /api/v1/passenger/push-devices` or `POST /api/v1/driver/push-devices`, body `{"token":"<FCM registration token>"}`. Failures retry while the process is alive (5 seconds through 5 minutes); foreground resume, token refresh and a new login trigger registration again. A token/account conflict rotates the installation token before retrying. Tokens/credentials are never logged.
- Backend registration binds encrypted tokens to a user and active session family. Logout/revocation removes those registrations; queued sends also check the session. An offline local sign-out cannot recall a notification already accepted by FCM.
- Existing passenger ride/account/promo messages use the delivery queue. Drivers now receive hail offers, passenger/admin cancellations, staff assignment changes, and shared-seat reservation/release messages. Expired or reassigned offers are skipped before sending. Push is a hint: opening the app reloads authoritative ride state; it does not accept a ride or navigate directly to an unverified payload ID.
- Firebase notification messages display through the SDK when the app is backgrounded. The service posts foreground messages. Notification taps open the app's normal authenticated navigation. Passenger inbox/foreground polling remains available independently; the driver has no new inbox UI in this change.
- Admin → Notification deliveries includes an announcement form for active passengers, drivers, or both. Support cannot send. `POST /api/v1/admin/notifications/announcements` accepts `audience` (`both`, `passengers`, `drivers`), `title` (1–160 characters) and `body` (1–500 characters). Required `Idempotency-Key`: 8–100 characters. A transaction stores the inbox entries, push jobs and deterministic staff audit receipt. Retry the same content/key for the original result; different content with that key returns 409. The audience is snapshotted once. Response 202: `{"id":"UUID","notified_count":42,"push_mode":"fcm"}` (`none` if disabled). Counts are accounts queued, not handset deliveries.

## Server prerequisites

Use the same Firebase project as both Android `google-services.json` files. The two local configs were checked to have matching project IDs. In Firebase Console:

1. Verify Android registrations `com.example.ridepassenger2` and `com.example.ridedriver` belong to that project.
2. Under Project settings → Cloud Messaging, ensure the Firebase Cloud Messaging HTTP v1 API is enabled.
3. Provision a Google service account with permission to send FCM messages in that project (Firebase Cloud Messaging API Admin role). To download a JSON key in Firebase Console, open Project settings → Service accounts → Firebase Admin SDK → Generate new private key, then keep the downloaded file private. Alternatively use Google Application Default Credentials/workload identity when supported by the hosting environment. A Google OAuth Web secret and Android `google-services.json` cannot authenticate the backend sender.

See [Firebase server authorization](https://firebase.google.com/docs/cloud-messaging/auth-server) and [Android setup](https://firebase.google.com/docs/cloud-messaging/android/get-started).

## Docker/VPS activation

The existing `docker-compose.fcm.yml` mounts a read-only service-account file into the API container. The current deploy script uses `/root/RideBackend-Rewrite-` on the VPS. On the server, place the private JSON at `secrets/firebase-service-account.json` under the backend checkout. The API runs as UID 10001, so the mounted file must be readable by that UID. Restrict the host directory to the operator and the file to the service user (for example directory mode 0700, file owner 10001:10001 and mode 0400). Do not commit this credential or copy it into either Android app.

Merge these entries into the server's existing `.env`; preserve other values:

```dotenv
# Match project_info.project_id from the Android configs.
FIREBASE_PROJECT_ID=<your Firebase project ID>
# Generate once with openssl rand -hex 32 and retain securely.
PUSH_TOKEN_ENCRYPTION_KEY=<64 hexadecimal characters>
PUSH_PROVIDER=fcm
# Keep the credentials mount enabled across ordinary automated deployments.
COMPOSE_FILE=docker-compose.yml:docker-compose.fcm.yml
```

The overlay supplies `GOOGLE_APPLICATION_CREDENTIALS=/run/secrets/firebase-service-account.json`. Keep the token encryption key stable after devices register. Do not print credentials or live `.env` values in logs. The Linux `COMPOSE_FILE` separator is `:`. This also makes the existing GitHub deploy script's ordinary `docker compose` commands load the FCM overlay on later deployments; a one-time explicit overlay command alone would not persist that choice.

After the changed backend code has been deployed, server activation commands for the operator are:

```bash
docker compose config -q
docker compose up -d --no-deps api
docker compose exec -T api wget -q -O - http://127.0.0.1:3000/ready
```

Run activation after successful backend deployment. No schema migration was added: notification tables and staff audit receipts already exist. Deploy both updated Android apps and the admin UI through the owner's normal release process. Do not generate APKs or run builds without the owner's request.

To disable push, remove the `COMPOSE_FILE` override, set `PUSH_PROVIDER=none`, and recreate the API with base Compose. Stored passenger inbox messages remain available. Keep encryption key backups if registered tokens will be reused later.

## Acceptance on real phones

1. Open each updated app with Google Play services available; sign in and allow notifications. Registration retries once the server is configured. Android Settings can restore permission if it was previously denied.
2. In admin, confirm **Push delivery: FCM**. Send an announcement to both apps; test with each app open and then in the background. Confirm a foreground alert, a system notification, and that tapping opens the correct app. Use a fresh announcement after devices register: old inbox entries are not retroactively pushed.
3. Exercise a real ride request with an eligible online driver, passenger cancellation, passenger ride updates and shared-seat changes. Driver offers can expire before delivery; the phone must fetch the current offer. FCM is not a guaranteed dispatch timer.
4. Sign out/revoke sessions and confirm subsequent account messages are not sent to that session. Test token rotation and reinstall/account changes.
5. Passenger `push_enabled=false` suppresses all queued push alerts; `ride_updates=false` suppresses ride/offer pushes, and `promotions=false` suppresses promotional pushes. Driver transport currently uses the same default push-enabled policy; Android system permission/channel settings provide its user control. Announcements respect global push enablement but are not promo campaigns.

`push_by_status.sent` means Firebase accepted a send, not that the phone displayed it. Check pending/failed/invalid counts, current registered sessions, Android permission/channel settings, network/Play services, matching Firebase project, service-account IAM and API availability. Delivery workers retry transient failures; data-only guaranteed background delivery is not claimed. Firebase Console test messages can check a specific installation, but bypass backend session, queue and preference checks.
