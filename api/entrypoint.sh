#!/bin/sh
set -e

# Run database migrations if DB_URL is set and atlas is available.
if [ -n "$DB_URL" ] && command -v atlas >/dev/null 2>&1 && [ -d /migrations ]; then
    echo "==> Running database migrations..."
    atlas migrate apply --dir "file:///migrations" --url "$DB_URL" 2>&1 || \
        echo "    (migrations already applied or failed — continuing)"
fi

echo "==> Starting API server..."
exec /server "$@"
