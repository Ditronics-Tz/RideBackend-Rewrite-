# Main-branch delivery

Work on `dev`. Pull requests targeting `main`, and pushes to `main`, run static checks and a build. Tests are deliberately excluded under the project's code-only rule. A merge into `main` automatically starts deployment after the checks succeed. Pushes to `dev` do not start this workflow. GitHub runner availability and build time determine when it finishes; deployment is not instantaneous.

## GitHub setup

Actions must be enabled. Both backend and admin repositories use existing secrets:

- `RIDA_DEPLOY_KEY`: the private SSH key paired with the server's restricted deployment key.
- `RIDA_DEPLOY_KNOWN_HOSTS`: the verified SSH host-key entry for `rida-backend.ditronics.co.tz`.

Never commit these values. Changes on `dev` do not deploy. To retry production, use **Actions → CI/CD → Run workflow → main**, or rerun the failed main run. A rerun for an older main commit is skipped if main has advanced. Avoid `[skip ci]` in release merge messages: it suppresses automatic delivery.

## Server entrypoint

The root-owned `deploy/gha-remote.sh` is installed as `/usr/local/sbin/rida-gha-deploy`. The existing authorized key forces this command and disables forwarding/PTY access. It accepts `backend <40-character SHA>`, `admin <40-character SHA>`, or the read-only `ready` command. Legacy `backend`/`admin` commands remain supported for operator use and deploy current main.

The server checkouts must remain clean and on `main`:

- Backend: `/root/RideBackend-Rewrite-`
- Admin: `/opt/rida-admin-frontend`

The entrypoint fetches main and only deploys the requested SHA if it is still the current main head. It fast-forwards without discarding local changes. A shared host lock serializes both repositories' deployments. It builds the new image while the current service remains available, preserves the running image as `rida-backend:previous` or `rida-admin-frontend:previous`, and waits for container health before reporting success. The workflow also checks the public HTTPS health endpoint. `GET /status` is served by this API, not by the admin application, and its history file stays on the uploads volume across a healthy release. See [docs/STATUS_PAGE.md](../docs/STATUS_PAGE.md).

Backend deployment creates a private PostgreSQL dump in `/root/rida-backups` before applying migrations. It replaces only the API, leaving the database container and persistent volumes in place. The FCM overlay is retained through the server's existing `COMPOSE_FILE` setting. Admin deployment replaces only its frontend container. Successful commit IDs are recorded in `/var/lib/rida-deploy/backend.sha` and `admin.sha`; these files indicate the last successful deployment, not the health of a subsequent failed attempt.

The entrypoint deliberately does not replace itself from Git during a deployment. When editing it, an operator must install the reviewed version on the server before workflows use new command syntax. From the local backend checkout:

```sh
bash -n deploy/gha-remote.sh
scp deploy/gha-remote.sh rida-server:/tmp/rida-gha-deploy.next
ssh rida-server 'bash -n /tmp/rida-gha-deploy.next && install -m 0755 -o root -g root /tmp/rida-gha-deploy.next /usr/local/sbin/rida-gha-deploy && rm /tmp/rida-gha-deploy.next'
```

## Failure recovery

A running container may reference an image ID that is no longer addressable in the local image store. This does not stop deployment. If available, the configured build image is retained under `:previous-build` instead of `:previous`, with an explicit warning. That build is not guaranteed to match the running container; verify its contents/schema compatibility before recovery. If neither reference exists, deployment reports that no recovery image was retained and continues building while the existing container stays running.

Read the failed Actions step first, then inspect `docker compose ps` and narrowly scoped service logs in the affected server checkout. A failed build leaves the running container intact. A failed migration stops deployment; inspect schema state before retrying. A health failure after replacement leaves the failed service for operator diagnosis; no automatic database or application rollback is attempted.

Prefer a corrective commit on `dev`, merge to `main`, and let delivery run again. Retained `:previous` images and database dumps support operator recovery, but an older binary may be incompatible with a newly applied schema. Do not restore a database or roll back an image without checking schema compatibility and the effect on writes since the backup. Do not prune named volumes or the retained recovery images during routine cleanup.
