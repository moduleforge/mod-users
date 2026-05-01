#!/bin/sh
set -e

# Run database migrations if DB_URL is set and goose is available.
# Migrations are assembled at build time into /app/schema/migrations/
# (core-model 0001-0012 + users-module 0100-0108).
if [ -n "$DB_URL" ] && command -v goose >/dev/null 2>&1 && [ -d /app/schema/migrations ]; then
    echo "==> Running database migrations..."
    goose -dir /app/schema/migrations postgres "$DB_URL" up 2>&1 || \
        echo "    (migrations already applied or failed — continuing)"
fi

# Refresh system CA bundle so any mounted dev-only CAs (e.g. local Authelia)
# are trusted by the OIDC discovery HTTP client. No-op in production.
update-ca-certificates 2>/dev/null || true

echo "==> Starting API server..."
exec /server "$@"
