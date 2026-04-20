# Multi-stage build for users-module API.
#
# Build context must be the outer user-components/ directory (not users-module/)
# because users-module/api's go.mod has local path replaces reaching
# ../../core-module/{model,api}. Docker compose sets context: ../../..
# which from users-module/deploy/local/ resolves to user-components/.
#
# Composed migrations: core-module/model (0000–0005) + users-module/model
# (0100–0109) are concatenated at build time into /app/schema/migrations/.

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Copy workspace + all four module manifests first for layer caching.
COPY go.work go.work.sum ./
COPY core-module/model/go.mod core-module/model/go.sum ./core-module/model/
COPY core-module/api/go.mod core-module/api/go.sum ./core-module/api/
COPY users-module/model/go.mod users-module/model/go.sum ./users-module/model/
COPY users-module/api/go.mod users-module/api/go.sum ./users-module/api/

# Download external deps. Local path replaces (core-model, core-api,
# users-module/model) are no-ops here — they resolve once source is copied.
RUN cd users-module/api && go mod download

# Copy source for all modules users-module/api transitively depends on.
COPY core-module/model/ ./core-module/model/
COPY core-module/api/ ./core-module/api/
COPY users-module/model/ ./users-module/model/
COPY users-module/api/ ./users-module/api/

RUN cd users-module/api && CGO_ENABLED=0 go build -o /server ./cmd/server

# Assemble composed migrations directly from the local source paths.
RUN mkdir -p /app/schema/migrations \
    && cp /src/core-module/model/migrations/*.sql /app/schema/migrations/ \
    && cp /src/users-module/model/migrations/*.sql /app/schema/migrations/

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata curl

# Install atlas CLI for migrations.
RUN curl -sSf https://atlasgo.sh | sh

COPY --from=builder /server /server
COPY --from=builder /app/schema/migrations /app/schema/migrations
COPY users-module/api/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
