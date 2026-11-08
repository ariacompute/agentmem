# syntax=docker/dockerfile:1
# Rebuild (and re-pull the base images) after the eMudhra/emSign CA key
# compromise (2026-10) so the shipped trust store no longer trusts those roots.
# Go 1.27.3 carries the crypto/x509 blocklist for the eight weak intermediates.

FROM golang:1.27.3-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/agentmem ./cmd/agentmem

FROM oven/bun:alpine AS frontend
WORKDIR /web
# Bun only has an outbound TLS surface while bun install runs; the built console
# is static and has no runtime trust store. Refresh the build-stage CA bundle.
RUN apk add --no-cache --upgrade ca-certificates
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ .
RUN bun run build

FROM alpine:3.21
RUN apk add --no-cache --upgrade ca-certificates tzdata \
    && addgroup -S agentmem \
    && adduser -S -D -H -G agentmem agentmem \
    && mkdir -p /app/data /app/web/dist \
    && chown -R agentmem:agentmem /app
WORKDIR /app
COPY --from=backend /out/agentmem /usr/local/bin/agentmem
COPY --chown=agentmem:agentmem --from=frontend /web/dist /app/web/dist
# Configuration comes from the environment (compose env_file: .env). The image
# deliberately contains no config file and no credentials. The service runs as
# an unprivileged user; only /app/data is writable.
ENV AGENTMEM_ADDR=0.0.0.0:8080
USER agentmem
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["agentmem"]
CMD ["serve"]
