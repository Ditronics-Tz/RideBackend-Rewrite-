#!/bin/bash
# Installed by an operator as /usr/local/sbin/rida-gha-deploy and used as
# a forced SSH command. Never evaluate SSH_ORIGINAL_COMMAND as shell code.
set -euo pipefail
umask 077

command="${1:-${SSH_ORIGINAL_COMMAND:-}}"
if [[ "$command" == ready ]]; then
  docker exec rida-backend-api-1 wget -q -O - http://127.0.0.1:3000/ready
  echo
  exit 0
fi
if [[ ! "$command" =~ ^(backend|admin)(\ ([0-9a-f]{40}))?$ ]]; then
  echo "Expected backend [commit], admin [commit], or ready" >&2
  exit 2
fi
action="${BASH_REMATCH[1]}"
revision="${BASH_REMATCH[3]:-}"

# Both repositories share one host: serialize builds, backups and migrations.
exec 9>/run/lock/rida-deploy.lock
flock -w 1200 9
trap 'echo "Deployment failed for $action at line $LINENO; inspect service health before retrying." >&2' ERR

case "$action" in
  backend) cd /root/RideBackend-Rewrite- ;;
  admin) cd /opt/rida-admin-frontend ;;
esac

if [[ "$(git branch --show-current)" != main || -n "$(git status --porcelain)" ]]; then
  echo "Deployment requires a clean main checkout; preserve and resolve server changes first." >&2
  exit 1
fi
git fetch origin main
latest="$(git rev-parse FETCH_HEAD)"
# Legacy commands without a revision deploy current main. CI always sends SHA.
revision="${revision:-$latest}"
if [[ "$revision" != "$latest" ]]; then
  echo "Skipping superseded $action run $revision; main is now $latest."
  exit 0
fi
git merge --ff-only "$revision"
[[ "$(git rev-parse HEAD)" == "$revision" ]]
docker compose config -q

preserve_image() {
  local container image configured
  container="$(docker compose ps -q "$1")"
  if [[ -n "$container" ]]; then
    image="$(docker inspect --format '{{.Image}}' "$container")"
    if docker image inspect "$image" >/dev/null 2>&1; then
      docker image tag "$image" "$2"
    else
      # A running container can outlive its original local image metadata.
      # Preserve the available build separately; never call it the running image.
      configured="$(docker inspect --format '{{.Config.Image}}' "$container")"
      if docker image inspect "$configured" >/dev/null 2>&1; then
        docker image tag "$configured" "${2}-build"
        echo "Warning: running image $image is unavailable; preserved $configured as ${2}-build. Verify compatibility before recovery." >&2
      else
        echo "Warning: no local recovery image is available for $1; the running container remains intact during build." >&2
      fi
    fi
  fi
}

if [[ "$action" == backend ]]; then
  preserve_image api rida-backend:previous
  # Build before taking a fresh backup or touching the running API/database.
  docker compose build api
  install -d -m 0700 /root/rida-backups
  backup="/root/rida-backups/ride-$(date -u +%Y%m%dT%H%M%SZ)-${revision:0:12}.dump"
  docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$backup.partial"
  [[ -s "$backup.partial" ]]
  mv "$backup.partial" "$backup"
  docker compose --profile tools run --rm --no-deps migrate up
  docker compose up -d --no-deps --wait --wait-timeout 120 api
  docker compose exec -T api wget -q -O - http://127.0.0.1:3000/ready
else
  preserve_image admin-frontend rida-admin-frontend:previous
  docker compose build admin-frontend
  docker compose up -d --no-deps --wait --wait-timeout 120 admin-frontend
  docker compose exec -T admin-frontend wget -q -O - http://127.0.0.1:8080/healthz
fi
echo
install -d -m 0700 /var/lib/rida-deploy
printf '%s\n' "$revision" > "/var/lib/rida-deploy/$action.sha"
echo "Deployed $action $revision successfully."
