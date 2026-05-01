#!/usr/bin/env bash
# shadow-db-lint.sh — apply migrations to an ephemeral Postgres container.
#
# Replaces `atlas migrate lint --dev-url …`. Spins up a throwaway Postgres
# instance, runs every migration in $1 against it, and tears down regardless
# of outcome. Exit code matches goose's exit code.
#
# Usage:  shadow-db-lint.sh <migrations-dir>
#
# Connectivity: connects via the container's Docker IP rather than relying on
# host port forwarding (which doesn't work in some sandboxed CI environments).

set -u
set -o pipefail

DIR="${1:-migrations}"

if [[ ! -d "$DIR" ]]; then
  echo "shadow-db-lint: directory not found: $DIR" >&2
  exit 2
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "shadow-db-lint: docker is required" >&2
  exit 2
fi

if ! command -v goose >/dev/null 2>&1; then
  echo "shadow-db-lint: goose is required (go install github.com/pressly/goose/v3/cmd/goose@latest)" >&2
  exit 2
fi

CNAME="goose-lint-$$-$(date +%s)"
trap 'docker rm -f "$CNAME" >/dev/null 2>&1 || true' EXIT

echo "shadow-db-lint: starting ephemeral Postgres ($CNAME)..."
if ! docker run -d --rm --name "$CNAME" \
       -e POSTGRES_PASSWORD=lint \
       postgres:16 >/dev/null; then
  echo "shadow-db-lint: failed to start container" >&2
  exit 2
fi

# Resolve container IP rather than relying on host port forwarding.
IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$CNAME")
if [[ -z "$IP" ]]; then
  echo "shadow-db-lint: could not resolve container IP" >&2
  exit 2
fi

URL="postgres://postgres:lint@${IP}:5432/postgres?sslmode=disable"

echo "shadow-db-lint: waiting for Postgres at ${IP}:5432..."
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if docker exec "$CNAME" pg_isready -U postgres >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker exec "$CNAME" pg_isready -U postgres >/dev/null 2>&1; then
  echo "shadow-db-lint: Postgres did not become ready in time" >&2
  exit 2
fi

echo "shadow-db-lint: applying $DIR via goose..."
goose -dir "$DIR" postgres "$URL" up
RC=$?

if [[ $RC -eq 0 ]]; then
  echo "shadow-db-lint: ok"
fi

exit $RC
