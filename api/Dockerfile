# Multi-stage build for mod-users API.
#
# Build context must be the outer user-components/ directory (not mod-users/)
# because mod-users/api's go.mod has local path replaces reaching
# ../../mod-core/{model,api}. Docker compose sets context: ../../..
# which from mod-users/deploy/local/ resolves to user-components/.
#
# Composed migrations: mod-core/model (0001–0012) + mod-users/model
# (0100–0108) are concatenated at build time into /app/schema/migrations/.

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Copy workspace + all four module manifests first for layer caching.
COPY go.work go.work.sum ./
COPY mod-core/model/go.mod mod-core/model/go.sum ./mod-core/model/
COPY mod-core/api/go.mod mod-core/api/go.sum ./mod-core/api/
COPY mod-users/model/go.mod mod-users/model/go.sum ./mod-users/model/
COPY mod-users/api/go.mod mod-users/api/go.sum ./mod-users/api/

# Download external deps. Local path replaces (core-model, core-api,
# mod-users/model) are no-ops here — they resolve once source is copied.
RUN cd mod-users/api && go mod download

# Copy source for all modules mod-users/api transitively depends on.
COPY mod-core/model/ ./mod-core/model/
COPY mod-core/api/ ./mod-core/api/
COPY mod-users/model/ ./mod-users/model/
COPY mod-users/api/ ./mod-users/api/

RUN cd mod-users/api && CGO_ENABLED=0 go build -o /server ./cmd/server

# Build the goose CLI from source so we can run it in the runtime image
# without needing curl-based installers.
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@latest

# Assemble composed migrations directly from the local source paths.
RUN mkdir -p /app/schema/migrations \
    && cp /src/mod-core/model/migrations/*.sql /app/schema/migrations/ \
    && cp /src/mod-users/model/migrations/*.sql /app/schema/migrations/

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /server /server
COPY --from=builder /go/bin/goose /usr/local/bin/goose
COPY --from=builder /app/schema/migrations /app/schema/migrations
COPY mod-users/api/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
