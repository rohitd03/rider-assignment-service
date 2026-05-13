# rider-assignment-service

A production-grade microservice that handles real-time rider assignment for a food delivery platform. When an order is placed, the service finds the nearest available rider using Redis geospatial search, atomically assigns them, advances the order through its lifecycle (CREATED → ASSIGNED → PICKED_UP → DELIVERED), and publishes each state change as an event to Kafka so downstream services — notifications, billing, analytics — can react without polling. The service is intentionally narrow in scope: it owns the assignment algorithm and the order state machine, and defers everything else (auth, rate-limiting, retries) to the infrastructure around it.

---

## Architecture

```
                            HTTP :8080
                                │
             ┌──────────────────┴──────────────────┐
             │    Logging Middleware                 │
             │    (request-id · slog JSON · timing)  │
             └──────────────────┬──────────────────┘
                                │
             ┌──────────────────┴──────────────────┐
             │                                      │
        OrderHandler                          RiderHandler
             │                                      │
      AssignmentService                        RiderService
        │        │        │                   │         │
        ▼        ▼        ▼                   ▼         ▼
   PostgreSQL  Redis    Kafka             PostgreSQL   Redis
   (orders,   (geo      (order-          (riders)    (geo index
    riders)    index,    events) ─────────────────────status keys)
               status              │
               keys)               ├──▶ downstream consumers
                                   └──▶ order-events.dlq

             GET /metrics ──▶ Prometheus scrape
             GET /health  ──▶ readiness probe (checks PG · Redis · Kafka)
```

---

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.26+ | https://go.dev/dl |
| Docker + Docker Compose | 24+ | https://docs.docker.com/get-docker |
| Make | any | pre-installed on macOS/Linux; `winget install GnuWin32.Make` on Windows |
| golangci-lint | 1.57+ | `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` |
| Postman | any | https://www.postman.com/downloads (optional, for manual testing) |

---

## Running locally

### 1. Configure environment

```bash
cp .env.example .env
# .env is pre-filled with defaults that work against the Docker Compose stack.
# No edits needed for a first run.
```

### 2. Start the stack

```bash
make run    # docker compose up --build
make stop   # docker compose down -v  ← also wipes DB volumes for a clean slate
```

This builds the Go binary inside Docker and starts PostgreSQL, Redis, Kafka (+ ZooKeeper), the service, Prometheus, and Grafana. The first run pulls images and may take a minute.

| Service | URL |
|---------|-----|
| API | http://localhost:8080 |
| Kafka (from host) | localhost:29092 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 (admin / admin) |

Wait for the service to log `"msg":"http server listening"` before sending requests.

> **Note:** The `.env` file stores localhost addresses for local development. When the app runs inside Docker Compose, the `environment:` block in `docker-compose.yml` automatically overrides them with Docker-internal hostnames (`postgres`, `redis`, `kafka`). No manual edits to `.env` are needed to run the stack.

### 3. Example requests

**Register a rider**
```bash
curl -s -X POST http://localhost:8080/api/v1/riders \
  -H "Content-Type: application/json" \
  -d '{"name":"Ravi Kumar","lat":12.9716,"lng":77.5946}' | jq
```

**Update rider location** *(also re-adds them to the geospatial index)*
```bash
curl -s -X POST http://localhost:8080/api/v1/riders/<rider-id>/location \
  -H "Content-Type: application/json" \
  -d '{"lat":12.9718,"lng":77.5950}' | jq
```

**Set rider online / offline**
```bash
# online
curl -s -X PATCH http://localhost:8080/api/v1/riders/<rider-id>/availability \
  -H "Content-Type: application/json" \
  -d '{"available":true}' | jq

# offline — removes rider from the geospatial index immediately
curl -s -X PATCH http://localhost:8080/api/v1/riders/<rider-id>/availability \
  -H "Content-Type: application/json" \
  -d '{"available":false}' | jq
```

**Create an order**
```bash
ORDER=$(curl -s -X POST http://localhost:8080/api/v1/orders \
  -H "Content-Type: application/json" \
  -d '{"pickup_lat":12.9716,"pickup_lng":77.5946}')
echo $ORDER | jq
ORDER_ID=$(echo $ORDER | jq -r '.id')
```

**Assign the nearest available rider** *(searches within 5 km)*
```bash
curl -s -X POST http://localhost:8080/api/v1/orders/$ORDER_ID/assign | jq
```

**Advance order status**
```bash
# rider picked up the order
curl -s -X PATCH http://localhost:8080/api/v1/orders/$ORDER_ID/status \
  -H "Content-Type: application/json" \
  -d '{"status":"PICKED_UP"}' | jq

# order delivered
curl -s -X PATCH http://localhost:8080/api/v1/orders/$ORDER_ID/status \
  -H "Content-Type: application/json" \
  -d '{"status":"DELIVERED"}' | jq
```

**Get order details**
```bash
curl -s http://localhost:8080/api/v1/orders/$ORDER_ID | jq
```

**Health check**
```bash
curl -s http://localhost:8080/health | jq
# {"status":"ok","dependencies":{"kafka":"healthy","postgres":"healthy","redis":"healthy"}}
```

### 4. Postman collection (alternative to curl)

A ready-made collection is included at `rider-assignment-service.postman_collection.json`. Import it into Postman and create a matching environment named **rider-assignment-service Local** with these variables:

| Variable | Initial value |
|----------|--------------|
| `base_url` | `http://localhost:8080` |
| `rider_id` | *(empty — auto-filled by Register Rider)* |
| `order_id` | *(empty — auto-filled by Create Order)* |

Select that environment before running. The requests are numbered 1–8 and designed to be run in order as a complete happy-path flow. `rider_id` and `order_id` are written to the environment automatically by the test scripts in Register Rider and Create Order.

---

## Running tests

```bash
make test
```

Runs the full test suite with the race detector. No external services required — all dependencies are mocked via interfaces.

Test coverage by layer:

| Package | What is tested |
|---------|----------------|
| `internal/domain` | All valid and invalid order state transitions (23 cases) |
| `internal/kafka` | Retry logic, DLQ routing, exact error string propagation |
| `internal/service` | AssignRider happy path and 10 failure modes; CreateOrder; UpdateOrderStatus |
| `internal/handler` | HTTP status codes and error codes for every endpoint via `httptest` |

---

## Key engineering decisions

### Why Redis geospatial instead of PostgreSQL for rider locations

Rider locations change continuously. Finding the nearest available rider requires a radius search over all active riders on every order assignment.

PostgreSQL can perform radius queries using the haversine formula, but doing so against an unindexed `lat`/`lng` column requires a full table scan. PostGIS solves that with a spatial index, but introduces an extension dependency and additional operational complexity.

Redis `GEOSEARCH` stores coordinates in a sorted set with a geohash encoding. A radius search runs in O(N + log M) where N is the number of results returned and M is the size of the set — effectively O(log M) for typical result sizes. Because the index lives in memory, latency is sub-millisecond even for thousands of riders.

This service uses Redis as a read cache for assignment and PostgreSQL as the write-through source of truth. `lat`/`lng` columns on the `riders` table ensure the position can be restored to Redis after a restart or eviction.

The status check (AVAILABLE vs BUSY) is a separate Redis key per rider (`rider:status:<id>`). After fetching up to 50 geo-sorted candidates in one `GEOSEARCH` call, all status keys are fetched in a single pipelined round trip — keeping the full assignment to two Redis commands regardless of candidate count.

### Why a Kafka dead letter queue instead of in-memory retries

When a Kafka publish fails, the service retries up to three times with exponential backoff (100 ms, 200 ms, 400 ms). If all retries are exhausted, the message is written to a separate `order-events.dlq` topic rather than discarded.

In-memory retry buffers do not survive process restarts, node failures, or OOM kills. Any event queued in memory at the moment of a crash is permanently lost, which means downstream consumers — billing, notifications — miss state changes silently.

The DLQ topic is durable. Each DLQ message carries the full original payload, the error reason, the retry count, and the timestamp of the first failure. This makes the DLQ replayable: an operator or automated job can re-publish messages to `order-events` once the broker recovers, with full context about what failed and when.

The service distinguishes between a hard Kafka failure (message not stored anywhere — logged at `ERROR`) and a successful DLQ write (`ErrPublishedToDLQ` — logged at `WARN`). The assignment operation itself succeeds in both cases because the database is the source of truth for order state; Kafka is a notification channel.

### How the order state machine prevents invalid transitions

The `domain` package owns the state machine. A package-level `validTransitions` map encodes every permitted edge:

```
CREATED   → ASSIGNED, CANCELLED
ASSIGNED  → PICKED_UP, CANCELLED
PICKED_UP → DELIVERED, FAILED
```

Terminal states (`DELIVERED`, `FAILED`, `CANCELLED`) are absent from the map. The `Transition(to OrderStatus)` method checks for the current status as a map key; a missing key means the order is in a terminal state, and the call returns `ErrInvalidTransition` regardless of the target.

The method mutates `order.Status` only on success. A failed transition leaves the order unchanged, so callers can safely attempt a transition before deciding whether to persist it.

There is also a second guard at the database level. The `AssignRider` SQL is:

```sql
UPDATE orders SET status = 'ASSIGNED', rider_id = $2
WHERE id = $1 AND status = 'CREATED'
```

The `AND status = 'CREATED'` predicate means that if two concurrent requests both pass the in-memory status check, only one succeeds at the database level — the other sees `RowsAffected = 0` and returns an error. This closes the TOCTOU window without requiring application-level distributed locking.

---

## Environment variables

| Variable | Required | Default | Description |
|----------|:--------:|---------|-------------|
| `PORT` | No | `8080` | TCP port the HTTP server listens on |
| `DATABASE_URL` | **Yes** | — | PostgreSQL DSN, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `REDIS_ADDR` | **Yes** | — | Redis server address as `host:port` |
| `KAFKA_BROKER` | **Yes** | — | Kafka bootstrap broker as `host:port` |
| `LOG_LEVEL` | No | `info` | Minimum log level: `debug` · `info` · `warn` · `error` |

> **Note:** In production this service runs behind an API gateway that handles authentication, TLS termination, and rate limiting. The service itself has no auth middleware by design — adding it here would duplicate gateway concerns and complicate service-to-service calls within the platform.
