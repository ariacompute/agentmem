# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/agentmem ./cmd/agentmem

FROM node:20-alpine AS frontend
WORKDIR /web
COPY web/package.json ./
RUN npm install --no-audit --no-fund
COPY web/ .
RUN npm run build

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend /out/agentmem /usr/local/bin/agentmem
COPY --from=frontend /web/dist /app/web/dist
# Configuration comes from the environment (compose env_file: .env). The image
# deliberately contains no config file and no credentials.
ENV AGENTMEM_ADDR=0.0.0.0:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["agentmem"]
CMD ["serve"]
