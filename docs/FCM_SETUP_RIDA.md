# Activate FCM for Rida passenger Android

The backend and admin are already deployed with a durable passenger inbox. FCM is an optional delivery channel for immediate Android push alerts. On 16 October 2026, the VPS did not contain a Firebase service-account JSON, so production is currently **inbox-only**. No actual handset push has been verified.

## 1. Firebase Console

1. Open the Firebase Console and select the Firebase project for Rida. The Google OAuth configuration previously supplied named `rida-511608`; verify that this is the actual Firebase project before using its ID.
2. Add an Android app with package/application ID exactly `com.example.ridepassenger2`.
3. In **Project settings → Cloud Messaging**, enable the Firebase Cloud Messaging HTTP v1 API.
4. Create a service account with the [Firebase Cloud Messaging API Admin role](https://firebase.google.com/docs/cloud-messaging/send/v1-api) for this project and download its JSON private key. Keep this file private. The Google OAuth Web client secret is not an FCM credential.
5. Download `google-services.json` for the Android app. Give that file to the mobile engineer; never give the server service-account JSON to the mobile app.

## 2. VPS secret and environment

Transfer the service-account JSON over SSH/SCP to `/root/RideBackend-Rewrite-/secrets/firebase-service-account.json`. The repository ignores `secrets/` and `.env`; neither is copied into the Docker image. On the VPS:

```bash
cd /root/RideBackend-Rewrite-
mkdir -p secrets
chmod 700 secrets
# Put the real service-account file here using a secure transfer first.
chown 10001:10001 secrets/firebase-service-account.json
chmod 400 secrets/firebase-service-account.json
openssl rand -hex 32
```

Copy the generated 64-character hex value into the existing `.env` as `PUSH_TOKEN_ENCRYPTION_KEY`. Add `FIREBASE_PROJECT_ID=<verified project ID>` to the same file. Do not replace the `.env` file or existing application secrets. Keep `PUSH_PROVIDER=none` in the base `.env`; the FCM Compose overlay sets the provider for the API container only. Protect `.env` with `chmod 600 .env`. Keep the encryption key stable after devices register.

Check the mount without printing the key:

```bash
docker compose -f docker-compose.yml -f docker-compose.fcm.yml config -q
docker compose -f docker-compose.yml -f docker-compose.fcm.yml run --rm --no-deps --entrypoint /bin/sh api -c 'test -r /run/secrets/firebase-service-account.json'
```

## 3. Activate the API

From the same VPS checkout:

```bash
docker compose -f docker-compose.yml -f docker-compose.fcm.yml up -d --no-deps api
docker compose -f docker-compose.yml -f docker-compose.fcm.yml ps
docker compose -f docker-compose.yml -f docker-compose.fcm.yml logs --tail=100 api
curl -fsS https://rida-backend.ditronics.co.tz/ready
```

The admin **Notification deliveries** page should show FCM enabled. If API startup fails, leave the durable inbox available by returning to the base Compose file: `docker compose up -d --no-deps api`. The admin UI does not need a rebuild when FCM is enabled.

## 4. Mobile engineer

Give the engineer [MOBILE_PROMOS_AND_NOTIFICATIONS.md](MOBILE_PROMOS_AND_NOTIFICATIONS.md). They must add the [Firebase Messaging Android SDK](https://firebase.google.com/docs/cloud-messaging/android/get-started), request Android 13+ notification permission, register and refresh device tokens through the authenticated Rida API, handle foreground/background notification taps, and display the durable inbox. Registration endpoint: `POST /api/v1/passenger/push-devices` with `{"token":"<FCM registration token>"}`. This endpoint needs a logged-in passenger. Do not put service-account JSON or private keys in Android.

For a real end-to-end check, sign in on one test phone, enable `promotions` and `push_enabled` in `/api/v1/passenger/preferences`, create and activate a promo in admin, then use **Share with audience**. Confirm the inbox entry, then the OS notification. Admin `sent` means Firebase accepted the request; it does not prove display on the handset.

## Alternative without FCM

The inbox and promo validation work with `PUSH_PROVIDER=none`. The Android app can sync on launch, foreground resume, and periodically with WorkManager. The [Android WorkManager minimum periodic interval is 15 minutes](https://developer.android.com/develop/background-work/background-tasks/persistent/getting-started/define-work); OS scheduling can delay it further. This is suitable for non-urgent promo updates but not instant background alerts. For instant OS notifications on standard Android, an actual push transport is needed; FCM is the most direct choice for this app.
