# Staff authenticator login and recovery

Admin and support passwords never return a staff session by themselves. A correct password returns a five-minute challenge. The account enrolls an authenticator on the first success, and later sign-ins submit a current code or a one-time recovery code. Challenge tokens are not staff JWTs: send them only to the MFA routes below. All paths use the `/api/v1` prefix. Responses set `Cache-Control: no-store`.

Apply migration 11 before this flow. It adds `staff_mfa_challenges`, `staff_mfa_recovery_codes`, and the authenticator step columns on `staff_users`. `STAFF_TOTP_KEY` must be exactly 32 bytes, either a raw 32-byte value or 64 hex characters (`openssl rand -hex 32`). The API encrypts the TOTP secret with AES-256-GCM under that key. Keep the same key across deploys. A different key makes an enrolled secret undecryptable: password login then returns `401` `Invalid credentials` and does not issue a challenge. Another administrator resets that account so it can enroll again. Recovery codes cannot help until a verify challenge is issued.

`STAFF_REQUIRE_2FA_FOR_ADMIN` defaults to true. It does not turn password-only login back on. While it is true, `StaffProtected` and staff role checks reject an access token for an unenrolled admin or support account on every route except `POST /staff/auth/logout` and `POST /staff/auth/logout-all`, with `403` and `require_2fa: true`. Refresh of an unenrolled admin or support session is rejected even when the flag is false. Account holders cannot disable the authenticator: `POST /staff/auth/2fa/disable` returns `403` `Two-factor authentication is required for this account`.

## Sign in

`POST /staff/auth/login` with `{"email":"admin@example.com","password":"..."}`. The `totp_code` field on this body is ignored.

A valid active account returns `200`:

```json
{"mfa_required":true,"enrollment_required":false,"challenge_token":"<challenge>","challenge_expires_in":300}
```

`enrollment_required` is true when no authenticator is active. Unknown email, wrong password, a locked account, and a deactivated account all return `401` `Invalid credentials`. Login attempts are limited to 10 per minute per IP and per email inside each API process (`429`, `Retry-After: 60`). Five failed password or authenticator attempts lock the account for 15 minutes. The lock is shared across API instances; the per-minute login cap is not.

| Next call | When | Body |
| --- | --- | --- |
| `POST /staff/auth/mfa/enroll` | `enrollment_required` is true | `{"challenge_token":"<challenge>"}` |
| `POST /staff/auth/mfa/enroll/confirm` | After enroll, with a code from that secret | `{"challenge_token":"<challenge>","code":"123456"}` |
| `POST /staff/auth/mfa/verify` | Authenticator already active | `{"challenge_token":"<challenge>","code":"123456"}` |
| `POST /staff/auth/mfa/recovery/verify` | Authenticator active, device unavailable | `{"challenge_token":"<challenge>","recovery_code":"ABCD-EFGH-JKLM-NPQR"}` |

Enroll returns the pending secret once, plus `qr_code_url`, `pending_expires_at`, `issuer` `Rida`, `algorithm` `SHA1`, `digits` 6, and `period` 30. Put that `otpauth://` URL in any RFC 6238 app. Repeating enroll before `pending_expires_at` returns the same secret. The pending secret lasts 10 minutes, which can outlive the 5-minute challenge: sign in again and call enroll to see the secret that confirm will accept. Confirm and verify accept the current 30-second step and one step on either side of server time. Each accepted step can be used once.

Confirm and verify return the staff session: `access_token`, `refresh_token`, `must_change_password`, and `user`. Confirm also returns `recovery_codes`, eight codes shown only in that response. If confirm returns `500` `Unable to create recovery codes` after the authenticator was stored, sign in again and use `mfa/verify`, then create codes with the signed-in regenerate route.

A recovery code is 16 characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, displayed as four hyphenated groups. Hyphens and spaces are ignored. Each code works once. Using one does not mint replacements. Codes are stored only as SHA-256 hashes.

MFA routes reject bodies larger than 4096 bytes (`400`). They allow 30 attempts per 5 minutes per IP and 20 per 5 minutes per account (`429`, `Retry-After: 60`), counted in the database across instances. A wrong code, a replayed step, or a bad recovery code returns `401` `Invalid verification code` and counts toward both the challenge and the 15-minute account lock. The challenge itself is consumed after 5 failures. An expired or consumed challenge returns `401` with `code` `mfa_challenge_expired`. Sign in again for a new challenge. A database failure on the challenge or limiter returns `503` `authentication temporarily unavailable`.

## Signed-in maintenance

These routes need a staff bearer token from a completed challenge.

| Method and path | Proof | Result |
| --- | --- | --- |
| `GET /staff/auth/mfa/status` | Session | `totp_enabled`, `enrollment_required`, `recovery_codes_remaining`, `totp_verified_at` |
| `GET /staff/auth/mfa/activity` | Session | Up to 20 recent security audit rows: `action`, `ip`, `created_at` |
| `POST /staff/auth/mfa/recovery/regenerate` | `{"code":"<current totp>"}` | Eight new `recovery_codes`; every previous code stops working |
| `POST /staff/auth/mfa/authenticator/replace` | `{"password":"...","code":"<current totp>"}` | New pending secret and `qr_code_url`. `POST /staff/auth/2fa/setup` is the same handler |
| `POST /staff/auth/mfa/authenticator/replace/confirm` | `{"code":"<code from the new secret>"}` | Activates that secret, rotates recovery codes, revokes other staff sessions, and returns a new `access_token` for this session. `POST /staff/auth/2fa/verify` is the same handler |

Replace and regenerate are limited to 10 calls per 10 minutes per account. Replacing the authenticator bumps `auth_version`, so older access tokens fail immediately. Keep the `access_token` from the confirm response.

## Lost authenticator

A second administrator calls `POST /admin/staff/:id/reset-mfa`. The caller cannot reset their own authenticator (`400` `Ask another administrator to reset your authenticator`). Support cannot call this route. Reset clears the secret, the pending secret, and unused recovery codes, consumes open challenges, bumps `auth_version`, and revokes every session. The next password login is an enrollment challenge. The message is `Authenticator reset. The staff member must set it up again at the next sign-in.`

## Troubleshooting

| What you see | What it usually means |
| --- | --- |
| `401` `Invalid credentials` with the right password | Account lockout, deactivated staff, or a `STAFF_TOTP_KEY` that cannot decrypt the stored secret. Check `staff_audit_logs` for `account_lockout` and `login_failure` with `totp_secret_unavailable`. |
| `401` `mfa_challenge_expired` | The 5-minute challenge ended, or 5 bad codes consumed it. Start again at login. |
| Confirm keeps failing after scanning a QR | The pending secret changed after 10 minutes, or the phone clock is more than one 30-second step away from the API. Enroll again and scan the new URL. |
| Second submit of the same 6-digit code fails | That step was already accepted. Wait for the next code. |
| Dashboard calls return `403` `require_2fa: true` | The bearer token belongs to an unenrolled admin or support account. Finish enrollment, or have another admin reset MFA. |
| Refresh returns `401` `Invalid refresh token` right after a password reset or authenticator change | Sessions were revoked or the auth version moved. Sign in from the password step. |
