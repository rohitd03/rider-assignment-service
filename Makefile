.PHONY: run stop test lint build tidy \
        minikube-start minikube-stop minikube-status minikube-logs minikube-health

## run: build and start the full stack (postgres, redis, kafka, app, prometheus, grafana)
run:
	docker compose up --build

## stop: tear down all containers and remove anonymous volumes
stop:
	docker compose down -v

## test: run all tests with the race detector enabled
test:
	go test -race ./...

## lint: run golangci-lint (install from https://golangci-lint.run/usage/install/)
lint:
	golangci-lint run ./...

## build: compile a local binary to bin/server
build:
	mkdir -p bin
	go build -ldflags="-s -w" -o bin/server ./cmd/server

## tidy: remove unused dependencies and tidy go.sum
tidy:
	go mod tidy

# ── Minikube targets ──────────────────────────────────────────────────────────

## minikube-start: start minikube (if needed), build image, apply manifests, wait for pods
minikube-start:
	@echo "==> Checking minikube..."
	@minikube status >/dev/null 2>&1 || minikube start --memory=4096 --cpus=2
	@echo "==> Building rider-assignment-service:local inside minikube Docker..."
	@eval $$(minikube docker-env) && docker build -t rider-assignment-service:local .
	@echo "==> Applying Kubernetes manifests..."
	@kubectl apply -f k8s/
	@echo "==> Waiting for postgres..."
	@kubectl rollout status deployment/postgres -n rider-assignment --timeout=120s
	@echo "==> Waiting for redis..."
	@kubectl rollout status deployment/redis -n rider-assignment --timeout=120s
	@echo "==> Waiting for kafka..."
	@kubectl rollout status deployment/kafka -n rider-assignment --timeout=180s
	@echo "==> Waiting for rider-assignment..."
	@kubectl rollout status deployment/rider-assignment -n rider-assignment --timeout=120s
	@echo ""
	@echo "All pods are ready. Run this in a separate terminal to expose the service:"
	@echo "  kubectl port-forward -n rider-assignment svc/rider-assignment 8080:80"
	@echo ""
	@echo "Then verify with:  make minikube-health"

## minikube-stop: delete all resources in the rider-assignment namespace
minikube-stop:
	@echo "==> Deleting rider-assignment namespace..."
	@kubectl delete namespace rider-assignment --ignore-not-found
	@echo "Namespace deleted. To also stop minikube run: minikube stop"

## minikube-status: show pods and services in the rider-assignment namespace
minikube-status:
	@echo "=== Pods ==="
	@kubectl get pods -n rider-assignment
	@echo ""
	@echo "=== Services ==="
	@kubectl get svc -n rider-assignment

## minikube-logs: tail app logs from the rider-assignment namespace
minikube-logs:
	@kubectl logs -n rider-assignment -l app=rider-assignment -f --tail=50

## minikube-health: port-forward, hit /health, print response, kill port-forward
minikube-health:
	@kubectl port-forward -n rider-assignment svc/rider-assignment 8181:80 & \
	PF_PID=$$!; \
	sleep 3; \
	echo "==> GET http://localhost:8181/health"; \
	curl -s http://localhost:8181/health; \
	echo ""; \
	kill $$PF_PID 2>/dev/null; true
