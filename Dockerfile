# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Download dependencies first — cached as a separate layer so code changes
# don't invalidate the module download step.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
        -ldflags="-s -w" \
        -o server \
        ./cmd/server

# ── Run stage ─────────────────────────────────────────────────────────────────
FROM alpine:3.19

# Non-root user for least-privilege execution.
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /app/server .

USER appuser

EXPOSE 8080

ENTRYPOINT ["./server"]
