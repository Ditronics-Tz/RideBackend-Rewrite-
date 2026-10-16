# Docker deployment on Ubuntu

## Architecture

The Compose project runs the Go 1.27.2 Fiber API and PostgreSQL 16 on a private
Docker bridge network. Only the API is published, bound to `127.0.0.1` by
default so a host reverse proxy can serve it. PostgreSQL has no host port. The
API runs as UID 10001 with a read-only root filesystem; writable uploads and
exports use a named volume. The database data and its generated TLS material
use separate named volumes. PostgreSQL TLS is verified by the API and migration
command.

The API checks database readiness at `/ready` and exits if the schema is not at
the embedded Goose migration version. Migrations are a separate, explicit step;
the API never changes the schema during startup. The migration command takes an
advisory lock and validates existing schema before applying the next migration.
The current schema version is 13. The ride dispatch worker also activates due
scheduled rides; it runs with the API even when optional `RUN_BACKGROUND_JOBS`
is false. Private support attachments are stored in `uploads_data` with local
storage and must be included in upload backups.

## Prerequisites

- Ubuntu 22.04 or newer on a supported 64-bit VPS; allow at least 2 GB RAM for
  the default container limits, plus disk for PostgreSQL and uploads.
- Docker Engine and the Compose V2 plugin. Install using Docker's
  [official Ubuntu Engine instructions](https://docs.docker.com/engine/install/ubuntu/)
  and [Compose plugin instructions](https://docs.docker.com/compose/install/linux/).
- A domain and host reverse proxy with HTTPS if the API will be public. Compose
  does not provide HTTPS.
- Production values for both JWT secrets, the staff TOTP key, the two database
  passwords, CORS origins, and approved ride fare configuration.

Do not stop or reconfigure other VPS containers as part of this deployment.
Choose a unique `COMPOSE_PROJECT_NAME` or edit the Compose `name` if another
deployment already uses `rida-backend` on this host.

## Configure the environment

From the backend checkout, create `.env` only if it does not already exist:

```bash
cp .env.example .env
chmod 600 .env
```

If `.env` already exists, edit the required settings in place and preserve its
existing values. Do not copy `.env.example` over it. Generate three independent
secrets with `openssl rand -hex 32`: `JWT_SECRET`, `STAFF_JWT_SECRET`, and
`STAFF_TOTP_KEY`. The app and staff JWT values must differ; the TOTP value must
be 64 hex characters (32 bytes). Generate distinct long random values for
`DB_ADMIN_PASSWORD` and `DB_PASSWORD`. Replace every `CHANGE_ME` value.
Keep `DB_ADMIN_USER` and `DB_USER` distinct; the Compose initialization script
refuses to reuse the administrator role for the API.

The repository ignore rules exclude `.env` and `.env.*` while allowing
`.env.example`. Removing `.env` from the Git index does not erase older Git
history; if any value was committed and remains valid, rotate it through the
normal credential process. The local `.env` file is preserved.

Set `CORS_ALLOWED_ORIGINS` to exact browser origins, without paths or wildcard
characters, for example `https://admin.example.com`. Set `PUBLIC_BASE_URL` to
the public API URL. Configure the approved `RIDE_*_TZS` values before enabling
real rides. Leave `STORAGE_DRIVER=local` for named-volume storage, or configure
all R2 variables before changing it to `r2`. Set `GOOGLE_WEB_CLIENT_ID` to the
Google Web OAuth client ID before enabling passenger Android Google sign-in.
The Android ID-token flow does not need `GOOGLE_CLIENT_SECRET`; that setting is
only for the separate browser redirect flow. The Google Web client ID is public,
but all client secrets and issued tokens must stay out of the repository and app.
Use a secret manager instead of a plaintext environment file when one is
available. Docker administrators can inspect container environment variables.

Compose uses `DB_ADMIN_USER` and `DB_ADMIN_PASSWORD` only for PostgreSQL's
bootstrap and the one-shot schema migration service. The API uses the separate,
less-privileged `DB_USER` and `DB_PASSWORD` account. The database initialization
script creates the application role and default table/sequence grants on a new
database volume. Existing database volumes are not modified by that script;
provision equivalent roles/grants deliberately before connecting an existing
database.

The PostgreSQL container creates a self-signed certificate with the `db` DNS
name on each start. It keeps the private key in a volume only the database can
read; the API receives only the public certificate and uses `verify-full`.
This setup is for the isolated Compose bridge. If you replace the database with
an external service, configure its CA trust and `DB_SSLMODE` for that service.

## Build, migrate, and start

The first deployment should build the images, start PostgreSQL, apply migrations,
then create the initial admin account if this is a new installation, then start
the API:

```bash
docker compose config -q
docker compose build
docker compose up -d db
docker compose ps
docker compose --profile tools run --rm migrate up
docker compose --profile tools run --rm create-admin --name "Initial Admin" --email "admin@example.com"
docker compose up -d api
```

Migration output reports the applied and latest schema versions. Stop and
investigate if the migration reports incompatible schema; do not attempt a
manual schema reset or rollback. `create-admin` is a one-time command for a new
installation and refuses to create another active admin. It receives
`STAFF_BOOTSTRAP_PASSWORD` only in that temporary command container. Later
deployments use the same order: build, run the one-shot migration, then recreate
the API with `docker compose up -d api`.

Useful checks and operations:

```bash
docker compose config -q
docker compose ps
docker compose logs -f --tail=100 api db
docker compose exec api wget -q -O - http://127.0.0.1:3000/ready
docker compose restart api
docker compose down
```

`docker compose down` stops and removes project containers and its bridge
network while retaining named volumes. Do not use `docker compose down -v` for
routine deployment: `-v` deletes the PostgreSQL, TLS, and uploads volumes.

## Pulling and rebuilding

Fetch the approved Git revision, then review the changes and environment
requirements before running:

```bash
git pull --ff-only
docker compose config -q
docker compose build
docker compose --profile tools run --rm migrate up
docker compose up -d api
docker compose ps
```

The Docker build excludes `.env`, `.git`, uploads, test output, and local build
artifacts. The SQL and schema JSON files remain in the Go build context because
the migration package embeds them in the binaries.

## Backups and persistent data

The `postgres_data` volume contains the database. The `uploads_data` volume
contains public uploads, private objects, legacy documents, and generated data
exports when local storage is enabled. `postgres_tls_public` contains only the
certificate used by clients; `postgres_tls_private` contains the database TLS
private key. Back up PostgreSQL and required uploads to encrypted off-host
storage and test restoration regularly. Example PostgreSQL dump:

```bash
mkdir -p backups
docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > backups/ride-$(date -u +%Y%m%dT%H%M%SZ).dump
```

Back up `uploads_data` separately using a volume-aware backup tool. Do not back
up the TLS private-key volume as application data. Protect backups as sensitive
data and document retention and restore ownership.

## Reverse proxy and HTTPS

The default `API_BIND_ADDRESS=127.0.0.1` and `API_PORT=3000` publish the API only
to the VPS loopback interface. Configure the existing host proxy to forward
HTTPS traffic to `http://127.0.0.1:3000`; preserve the original `Host` and
forwarded protocol/client headers. Configure the matching HTTPS origin in
`CORS_ALLOWED_ORIGINS`. If the proxy runs in another container, it cannot use
its own `127.0.0.1` to reach this API; arrange a shared, explicitly managed
Docker network or a host-gateway route and firewall it appropriately.

To bind another address or host port, set `API_BIND_ADDRESS` and `API_PORT` in
`.env`. Binding to `0.0.0.0` exposes the unencrypted API directly on all host
interfaces; use only behind a deliberate firewall/proxy configuration.

## Troubleshooting

- **API exits saying schema is not current:** check `docker compose logs db`, run
  `docker compose --profile tools run --rm migrate status`, then apply with
  `docker compose --profile tools run --rm migrate up`. Review incompatible
  schema errors before proceeding.
- **Database health check fails:** inspect `docker compose logs --tail=100 db`,
  verify `DB_ADMIN_PASSWORD`, and allow the initial database to finish setup.
- **TLS verification fails:** ensure API and migration mounts include the
  generated `postgres_tls_public` volume, and that `DB_HOST` is `db` when using
  this Compose PostgreSQL service. Do not disable TLS to work around a mismatch.
- **Port already in use:** change `API_PORT` or choose another loopback binding.
- **Uploads fail with permission errors:** retain the named `uploads_data`
  volume and verify the API runs as UID 10001. Do not make private upload
  directories world-readable.
- **No space left on device:** inspect Docker's disk usage, PostgreSQL growth,
  upload volume growth, and rotated container logs before cleaning anything.

Compose configures the `json-file` driver with a default maximum of 10 MB per
file and five files per service. Adjust `DOCKER_LOG_MAX_SIZE` and
`DOCKER_LOG_MAX_FILE` in `.env` to match the VPS disk budget. Docker daemon-wide
logging policy may override per-container settings.
