# API changes

## 2026-10-13 - B1

Production startup now requires APP_ENV=production and independent JWT_SECRET /
STAFF_JWT_SECRET values of at least 32 bytes, rejecting example-like values.
Development remains the default. No endpoint or request payload changes.
Affected: deployment owner; admin/passenger/driver clients are unchanged.
