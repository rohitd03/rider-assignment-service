# Contributing to rider-assignment-service

## Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | 1.26+ | https://go.dev/dl/ |
| Docker Desktop | latest | https://www.docker.com/products/docker-desktop/ |
| minikube | 1.32+ | https://minikube.sigs.k8s.io/docs/start/ |
| kubectl | 1.28+ | https://kubernetes.io/docs/tasks/tools/ |
| helm | 3.14+ | https://helm.sh/docs/intro/install/ |

Verify everything is on your `PATH` before starting:

```bash
go version && docker info && minikube version && kubectl version --client && helm version
```

---

## Way 1 — Docker Compose (recommended for most developers)

The fastest way to get a fully-functional local stack. Everything runs in containers; no minikube required.

```bash
# Start all services (postgres, redis, kafka, app, prometheus, grafana)
docker compose up --build

# Run in background
docker compose up --build -d

# Tail app logs only
docker compose logs -f app
```

Once started, verify all dependencies are healthy:

```bash
curl http://localhost:8080/health
# Expected: {"status":"ok","dependencies":{"kafka":"healthy","postgres":"healthy","redis":"healthy"}}
```

### Service ports

| Service | URL |
|---|---|
| API | http://localhost:8080 |
| Kafka (from host) | localhost:29092 |
| PostgreSQL | localhost:5432 |
| Redis | localhost:6379 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 (admin / admin) |

### Stopping

```bash
docker compose down          # stop containers, keep volumes
docker compose down -v       # stop containers AND delete postgres data
```

---

## Way 2 — Minikube (for Kubernetes validation)

Use this to validate the Kubernetes manifests behave correctly on a real cluster.

### Automated setup (recommended)

The setup script handles every step: pulling images, loading them into minikube (bypassing minikube's unreliable internet access), building the service image, and waiting for rollout.

```bash
bash scripts/minikube-setup.sh
```

When done, port-forward the service in a separate terminal:

```bash
kubectl port-forward -n rider-assignment svc/rider-assignment 8080:80
```

Then verify:

```bash
curl http://localhost:8080/health
```

### Manual steps (if you prefer step-by-step)

```bash
# 1. Start minikube
minikube start --memory=4096 --cpus=2

# 2. Pull dependency images via host Docker (has internet),
#    then load them into minikube's image cache
docker pull postgres:16-alpine && minikube image load postgres:16-alpine
docker pull redis:7-alpine     && minikube image load redis:7-alpine
docker pull apache/kafka:3.7.0 && minikube image load apache/kafka:3.7.0
docker pull busybox:1.36       && minikube image load busybox:1.36

# 3. Switch Docker context to minikube and build the app image
eval $(minikube docker-env)
docker build -t rider-assignment-service:local .

# 4. Apply all manifests
kubectl apply -f k8s/

# 5. Wait for pods to be ready
kubectl rollout status deployment/postgres        -n rider-assignment --timeout=120s
kubectl rollout status deployment/redis           -n rider-assignment --timeout=120s
kubectl rollout status deployment/kafka           -n rider-assignment --timeout=120s
kubectl rollout status deployment/rider-assignment -n rider-assignment --timeout=120s

# 6. Port-forward (keep this terminal open)
kubectl port-forward -n rider-assignment svc/rider-assignment 8080:80
```

### Resetting the cluster

```bash
# Delete only the app namespace and re-apply
bash scripts/minikube-setup.sh --reset

# Or wipe the whole cluster
minikube delete && minikube start
```

---

## Running tests

```bash
# All tests (unit + integration)
go test ./...

# With verbose output and race detector
go test -race -v ./...

# A specific package
go test ./internal/service/...

# With coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

Tests do not require Docker or minikube — they use in-process mocks.

---

## Linting

```bash
# Install (first time only — builds from source with local Go toolchain)
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8

# Run
golangci-lint run ./...
```

---

## Common troubleshooting

### Docker Compose

**`docker compose up` hangs waiting for kafka to be healthy**

Kafka takes ~30 seconds to elect a KRaft controller on first start. The app's `depends_on` waits for the health check to pass. Wait up to 90 seconds before concluding something is wrong.

```bash
# Watch kafka health status in real time
docker compose ps kafka
docker compose logs kafka
```

**Port already in use (5432 / 6379 / 8080)**

Stop any local PostgreSQL, Redis, or other services using those ports, or change the host-side mapping in `docker-compose.yml`.

**`DATABASE_URL` / `REDIS_ADDR` / `KAFKA_BROKER` not set**

These are set directly in the `docker-compose.yml` `environment:` block. No `.env` file is required. If you see `FATAL: required environment variable ... is not set`, the app service is not picking up the compose environment — ensure you are running `docker compose` (not `docker-compose`) v2.

---

### Minikube

**Pods stuck in `ImagePullBackOff`**

minikube's network may not reach the internet. Use the setup script or manually load images:

```bash
docker pull <image>
minikube image load <image>
```

**App pods stuck in `Init:0/2`**

The init containers (`wait-for-postgres`, `wait-for-redis`) are still waiting. Check that postgres and redis pods are Running:

```bash
kubectl get pods -n rider-assignment
kubectl logs -n rider-assignment -l app=postgres
```

**`/health` returns `kafka: unhealthy`**

If the kafka pod is in `ImagePullBackOff` or not yet ready, the app's health check will show kafka as unhealthy but the API (create order, assign rider, etc.) will still function. Load the kafka image manually:

```bash
docker pull apache/kafka:3.7.0
minikube image load apache/kafka:3.7.0
kubectl rollout restart deployment/kafka -n rider-assignment
```

**HPA shows `cpu: <unknown>`**

The metrics-server addon is not enabled. This is cosmetic — the HPA doesn't affect pod scheduling.

```bash
minikube addons enable metrics-server
```

**`eval $(minikube docker-env)` on Windows PowerShell**

The `eval` syntax is bash-only. Use Git Bash or WSL to run the setup script. Alternatively, run the PowerShell equivalent:

```powershell
& minikube -p minikube docker-env --shell powershell | Invoke-Expression
```
