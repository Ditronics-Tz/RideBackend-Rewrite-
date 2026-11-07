#!/bin/bash
# Restricted deploy entrypoint for GitHub Actions. The server installs this
# file as a forced SSH command. It only accepts the actions below.
set -euo pipefail

action="${1:-${SSH_ORIGINAL_COMMAND:-}}"

refresh_self() {
  local source="/root/RideBackend-Rewrite-/deploy/gha-remote.sh"
  if [[ "${RIDA_GHA_INNER:-}" == 1 || ! -f "$source" ]]; then
    return
  fi
  if ! cmp -s "$source" /usr/local/sbin/rida-gha-deploy; then
    install -m 0755 "$source" /usr/local/sbin/rida-gha-deploy
  fi
  RIDA_GHA_INNER=1 exec /usr/local/sbin/rida-gha-deploy "$action"
}

deploy_backend() {
  cd /root/RideBackend-Rewrite-
  git pull --ff-only
  refresh_self
  mkdir -p /root/rida-backups
  local stamp
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
    > "/root/rida-backups/ride-${stamp}.dump"
  docker compose config -q
  docker compose build
  docker compose --profile tools run --rm migrate up
  docker compose up -d api
  docker compose exec -T api wget -q -O - http://127.0.0.1:3000/ready
  echo
}

deploy_admin() {
  cd /opt/rida-admin-frontend
  git pull --ff-only
  docker compose config -q
  docker compose up -d --build
  docker compose exec -T admin-frontend wget -q -O - http://127.0.0.1:8080/healthz
  echo
}

case "$action" in
  backend) deploy_backend ;;
  admin) deploy_admin ;;
  ready)
    docker exec rida-backend-api-1 wget -q -O - http://127.0.0.1:3000/ready
    echo
    ;;
  *)
    echo "unsupported command" >&2
    exit 2
    ;;
esac
