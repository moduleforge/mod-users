#!/usr/bin/env bash
# shadow-db-lint.sh — apply migrations to an ephemeral Postgres container.
#
# Replaces `atlas migrate lint --dev-url …`. Spins up a throwaway Postgres
# instance, runs every migration in $1 against it, and tears down regardless
# of outcome. Exit code matches goose's exit code.
#
# Usage:  shadow-db-lint.sh <migrations-dir> [prereq-migrations-dir ...]
#
# Prereq dirs: a module's migrations may declare `migrations.after:
# [other-module]` in its manifest when they FK into that other module's
# tables. `mfgen` resolves this ordering for real deploys via its migration
# resolver, but this standalone lint script has no access to that resolver,
# so callers (e.g. CI) must supply prerequisite migration directories
# explicitly, in dependency order. Each prereq dir is applied in order
# before the primary dir, all against the same ephemeral database (sharing
# one goose_db_version tracking table — safe as long as version numbers
# don't collide across modules).
#
# Connectivity: the container publishes 5432 to an OS-assigned free port on
# 127.0.0.1 (`-p 127.0.0.1:0:5432`), and the script connects to that mapped
# host port. This works uniformly on both native Linux Docker Engine and
# Docker Desktop (macOS/Windows) — Docker Desktop runs containers inside a
# VM, so the container's bridge-network IP is not reachable from the host;
# only published ports are. Using an OS-assigned ephemeral port also avoids
# clashing with any real Postgres already listening on 5432.

set -u
set -o pipefail

DIR="${1:-migrations}"
shift || true
PREREQ_DIRS=("$@")

if [[ ! -d "$DIR" ]]; then
  echo "shadow-db-lint: directory not found: $DIR" >&2
  exit 2
fi

if [[ ${#PREREQ_DIRS[@]} -gt 0 ]]; then
  for pdir in "${PREREQ_DIRS[@]}"; do
    if [[ ! -d "$pdir" ]]; then
      echo "shadow-db-lint: directory not found: $pdir" >&2
      exit 2
    fi
  done
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
       -p 127.0.0.1:0:5432 \
       postgres:16 >/dev/null; then
  echo "shadow-db-lint: failed to start container" >&2
  exit 2
fi

# Resolve the OS-assigned host port that 5432 was published to.
PORT=$(docker port "$CNAME" 5432/tcp | head -n1 | sed -E 's/.*:([0-9]+)$/\1/')
if [[ -z "$PORT" ]]; then
  echo "shadow-db-lint: could not resolve published host port" >&2
  exit 2
fi

URL="postgres://postgres:lint@127.0.0.1:${PORT}/postgres?sslmode=disable"

echo "shadow-db-lint: waiting for Postgres on 127.0.0.1:${PORT}..."
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

if [[ ${#PREREQ_DIRS[@]} -gt 0 ]]; then
  for pdir in "${PREREQ_DIRS[@]}"; do
    echo "shadow-db-lint: applying prereq $pdir via goose..."
    goose -dir "$pdir" postgres "$URL" up
    RC=$?
    if [[ $RC -ne 0 ]]; then
      echo "shadow-db-lint: prereq apply failed: $pdir" >&2
      exit $RC
    fi
  done
fi

echo "shadow-db-lint: applying $DIR via goose..."
goose -dir "$DIR" postgres "$URL" up
RC=$?

if [[ $RC -eq 0 ]]; then
  echo "shadow-db-lint: ok"
fi

exit $RC
