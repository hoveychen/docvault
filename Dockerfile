# Multi-stage build → one slim image carrying both `server` and `worker` binaries
# plus the built frontend. Compose picks which binary to run per service.

# --- stage 1: frontend ---
FROM node:22-alpine AS web
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# --- stage 2: go build ---
FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# --- stage 3: runtime ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 docvault
WORKDIR /app
COPY --from=build /out/server /app/bin/server
COPY --from=build /out/worker /app/bin/worker
COPY --from=web /web/dist /app/web/dist
USER docvault
EXPOSE 8080
# Default to the API server; the worker service overrides this with command: ["/app/bin/worker"].
CMD ["/app/bin/server"]
