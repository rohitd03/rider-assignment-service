#!/usr/bin/env bash
# scripts/minikube-setup.sh
#
# One-shot script that gets rider-assignment-service running on a local
# minikube cluster from scratch. Run this instead of the manual steps.
#
# Requirements: minikube, kubectl, docker (Docker Desktop or equivalent)
#
# Usage:
#   bash scripts/minikube-setup.sh          # start everything
#   bash scripts/minikube-setup.sh --reset  # delete namespace and re-apply

set -euo pipefail

# ── Colour helpers ────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[setup]${NC} $*"; }
info() { echo -e "${CYAN}[info]${NC}  $*"; }
warn() { echo -e "${YELLOW}[warn]${NC}  $*"; }
die()  { echo -e "${RED}[error]${NC} $*" >&2; exit 1; }

# ── Prerequisite check ────────────────────────────────────────────────────────
for cmd in minikube kubectl docker; do
  command -v "$cmd" >/dev/null 2>&1 || die "$cmd is required but not installed. See CONTRIBUTING.md."
done

# ── Optional reset ────────────────────────────────────────────────────────────
if [[ "${1:-}" == "--reset" ]]; then
  warn "Deleting rider-assignment namespace..."
  kubectl delete namespace rider-assignment --ignore-not-found
  log "Namespace deleted. Proceeding with fresh install."
fi

# ── Start minikube ────────────────────────────────────────────────────────────
MINIKUBE_STATUS=$(minikube status --format='{{.Host}}' 2>/dev/null || true)
if [[ "$MINIKUBE_STATUS" != "Running" ]]; then
  log "Starting minikube (2 CPUs, 4 GB RAM)..."
  minikube start --memory=4096 --cpus=2
else
  log "minikube is already running."
fi

# ── Pull dependency images using the HOST Docker daemon ───────────────────────
# minikube's Docker daemon often has no direct internet access.
# We pull images via the host's Docker Desktop, then load them into minikube
# with `minikube image load`. This must happen BEFORE `eval minikube docker-env`
# switches the active Docker daemon to minikube's.
IMAGES=(
  "postgres:16-alpine"
  "redis:7-alpine"
  "apache/kafka:3.7.0"
  "busybox:1.36"
)

log "Pulling dependency images via host Docker..."
for img in "${IMAGES[@]}"; do
  if docker image inspect "$img" >/dev/null 2>&1; then
    info "$img already cached locally — skipping pull."
  else
    log "Pulling $img..."
    docker pull "$img"
  fi
done

log "Loading images into minikube (this may take a minute)..."
for img in "${IMAGES[@]}"; do
  log "  loading $img..."
  minikube image load "$img"
done

# ── Build the app image inside minikube's Docker daemon ───────────────────────
# After eval, all `docker` commands run inside minikube — so the built image
# is immediately available to k8s pods without a registry push.
log "Switching Docker context to minikube..."
eval "$(minikube docker-env)"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

log "Building rider-assignment-service:local inside minikube..."
docker build -t rider-assignment-service:local "$REPO_ROOT"

# ── Apply Kubernetes manifests ────────────────────────────────────────────────
log "Applying Kubernetes manifests..."
kubectl apply -f "$REPO_ROOT/k8s/"

# ── Wait for each deployment ──────────────────────────────────────────────────
NS="rider-assignment"
TIMEOUT="180s"

log "Waiting for pods to reach Running state (timeout: $TIMEOUT each)..."

for deploy in postgres redis kafka rider-assignment; do
  log "  waiting for $deploy..."
  if ! kubectl rollout status deployment/"$deploy" -n "$NS" --timeout="$TIMEOUT"; then
    warn "$deploy did not become ready within $TIMEOUT."
    warn "Inspect with:"
    warn "  kubectl describe pod -n $NS -l app=$deploy"
    warn "  kubectl logs -n $NS -l app=$deploy --tail=50"
    warn "Continuing — the app may still be healthy if only Kafka is slow."
  fi
done

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
log "Setup complete. Pod status:"
kubectl get pods -n "$NS"

echo ""
log "To access the service, run this in a separate terminal:"
info "  kubectl port-forward -n $NS svc/rider-assignment 8080:80"
echo ""
log "Then test:"
info "  curl http://localhost:8080/health"
info "  curl -s http://localhost:8080/health | python3 -m json.tool  (pretty-print)"
echo ""
log "To watch logs:"
info "  kubectl logs -n $NS -l app=rider-assignment -f"
echo ""
log "To tear down:"
info "  kubectl delete namespace $NS"
