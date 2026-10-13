# API changes

## 2026-10-13 - B1

Production startup now requires APP_ENV=production and independent JWT_SECRET /
STAFF_JWT_SECRET values of at least 32 bytes, rejecting example-like values.
Development remains the default. No endpoint or request payload changes.
Affected: deployment owner; admin/passenger/driver clients are unchanged.

## 2026-10-13 - B2

`/api/v1/uploads/*`: startup no longer publishes legacy non-licence files.
The one-shot `go run ./cmd/migrate-uploads --dry-run` reports counts only;
without --dry-run, database-confirmed photos can become public, known documents
remain private, and unknown files (including previously exposed public files)
move to private quarantine. Existing owner/staff document routes are retained;
migrated document URLs use the authenticated route. No mobile payload change.
Affected: deployment operator and any client holding an unsafe legacy public
document URL (refetch document metadata for its protected URL).
