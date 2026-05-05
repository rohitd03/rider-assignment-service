package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"rider-assignment-service/internal/handler"
	"rider-assignment-service/internal/kafka"
	appmetrics "rider-assignment-service/internal/metrics"
	"rider-assignment-service/internal/middleware"
	"rider-assignment-service/internal/repository"
	"rider-assignment-service/internal/service"
)

// config holds all values loaded from environment variables at startup.
// Required vars cause an immediate os.Exit(1) if absent.
type config struct {
	Port        string
	DatabaseURL string
	RedisAddr   string
	KafkaBroker string
	LogLevel    string
}

func main() {
	cfg := loadConfig()
	log := newLogger(cfg.LogLevel)

	log.Info("starting rider-assignment-service",
		"port", cfg.Port,
		"log_level", cfg.LogLevel,
	)

	ctx := context.Background()

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("failed to create postgres pool", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Error("postgres ping failed", "error", err)
		os.Exit(1)
	}
	log.Info("connected to postgres")

	// ── Redis ─────────────────────────────────────────────────────────────────
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer redisClient.Close()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	log.Info("connected to redis")

	// ── Kafka ─────────────────────────────────────────────────────────────────
	// kafka-go Writer connects lazily; no up-front ping is available.
	// The health endpoint monitors broker reachability at runtime.
	kafkaProducer := kafka.NewKafkaProducer(cfg.KafkaBroker)
	defer kafkaProducer.Close()
	log.Info("kafka producer initialised", "broker", cfg.KafkaBroker)

	// ── Prometheus ────────────────────────────────────────────────────────────
	registry := prometheus.NewRegistry()
	m, err := appmetrics.NewMetrics(registry)
	if err != nil {
		log.Error("failed to initialise metrics", "error", err)
		os.Exit(1)
	}

	// ── Repositories ──────────────────────────────────────────────────────────
	orderRepo := repository.NewPostgresOrderRepository(db)
	riderRepo := repository.NewPostgresRiderRepository(db)
	redisRepo := repository.NewRedisRepository(redisClient)

	// ── Services ──────────────────────────────────────────────────────────────
	assignmentSvc := service.NewAssignmentService(orderRepo, riderRepo, redisRepo, kafkaProducer, log)
	riderSvc := service.NewRiderService(riderRepo, redisRepo, log)

	// ── HTTP mux ──────────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	handler.NewOrderHandler(assignmentSvc).RegisterRoutes(mux)
	handler.NewRiderHandler(riderSvc).RegisterRoutes(mux)

	mux.Handle("GET /metrics", m.Handler())
	mux.HandleFunc("GET /health", healthHandler(db, redisClient, cfg.KafkaBroker))

	// ── Middleware ────────────────────────────────────────────────────────────
	loggingMw := middleware.NewLogger(log)

	// ── HTTP server ───────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      loggingMw.Wrap(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start serving in a background goroutine so we can listen for signals.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-quit:
		log.Info("shutdown signal received", "signal", sig.String())
	case err := <-serverErr:
		log.Error("server error", "error", err)
	}

	log.Info("draining in-flight requests", "timeout", "30s")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown timed out — forcing close", "error", err)
	}

	log.Info("server stopped cleanly")
	// Deferred Close() calls for db, redisClient, kafkaProducer run here.
}

// ── Config ────────────────────────────────────────────────────────────────────

func loadConfig() config {
	return config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: mustGetEnv("DATABASE_URL"),
		RedisAddr:   mustGetEnv("REDIS_ADDR"),
		KafkaBroker: mustGetEnv("KAFKA_BROKER"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// mustGetEnv exits immediately with a clear message if a required variable is
// absent. Called before the logger is initialised, so it writes to stderr.
func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "FATAL: required environment variable %q is not set\n", key)
		os.Exit(1)
	}
	return v
}

// ── Logger ────────────────────────────────────────────────────────────────────

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

// ── Health handler ────────────────────────────────────────────────────────────

type healthResponse struct {
	Status       string            `json:"status"`
	Dependencies map[string]string `json:"dependencies"`
}

// healthHandler returns a handler that probes each dependency and reports its
// status. Returns 200 when all are reachable, 503 otherwise.
// Used as both a k8s readiness probe and a live debugging tool.
func healthHandler(db *pgxpool.Pool, rdb *redis.Client, kafkaBroker string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		deps := make(map[string]string, 3)
		healthy := true

		// PostgreSQL
		if err := db.Ping(ctx); err != nil {
			deps["postgres"] = "unhealthy: " + err.Error()
			healthy = false
		} else {
			deps["postgres"] = "healthy"
		}

		// Redis
		if err := rdb.Ping(ctx).Err(); err != nil {
			deps["redis"] = "unhealthy: " + err.Error()
			healthy = false
		} else {
			deps["redis"] = "healthy"
		}

		// Kafka — TCP dial is sufficient; kafka-go connects lazily.
		conn, err := net.DialTimeout("tcp", kafkaBroker, 2*time.Second)
		if err != nil {
			deps["kafka"] = "unhealthy: " + err.Error()
			healthy = false
		} else {
			conn.Close()
			deps["kafka"] = "healthy"
		}

		status := "ok"
		httpStatus := http.StatusOK
		if !healthy {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		json.NewEncoder(w).Encode(healthResponse{
			Status:       status,
			Dependencies: deps,
		})
	}
}
