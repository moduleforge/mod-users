# Multi-stage build for users-module API.
# Build context must be the monorepo root (users-module/) because
# the api module depends on the model module via go.work.

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Copy workspace files first for better layer caching.
COPY go.work go.work.sum ./
COPY api/go.mod api/go.sum ./api/
COPY model/go.mod model/go.sum ./model/

RUN cd api && go mod download

# Copy source.
COPY api/ ./api/
COPY model/ ./model/

RUN cd api && CGO_ENABLED=0 go build -o /server ./cmd/server

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata curl

# Install atlas CLI for migrations.
RUN curl -sSf https://atlasgo.sh | sh

COPY --from=builder /server /server
COPY model/migrations /migrations
COPY api/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
