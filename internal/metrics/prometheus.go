package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"rider-assignment-service/internal/domain"
)

// Metrics holds all instrumentation for the service. Each field is unexported
// so callers must use the typed methods, which enforce correct label values.
type Metrics struct {
	registry           *prometheus.Registry
	assignmentDuration *prometheus.HistogramVec
	activeRiders       prometheus.Gauge
	ordersTotal        *prometheus.CounterVec
	kafkaPublishErrors prometheus.Counter
}

// NewMetrics registers all metrics with the provided registry and returns a
// ready-to-use Metrics instance. Returns an error if any metric name collides
// with an already-registered metric in the registry.
func NewMetrics(registry *prometheus.Registry) (*Metrics, error) {
	assignmentDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "order_assignment_duration_seconds",
			Help: "End-to-end duration of a rider assignment operation (geo-search + DB writes).",
			// Buckets tuned for sub-second geo+DB operations; captures outliers up to 2.5 s.
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
		},
		[]string{"status"}, // "success" | "failure"
	)

	activeRiders := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "active_riders_total",
		Help: "Current number of riders in AVAILABLE state.",
	})

	ordersTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orders_total",
			Help: "Cumulative number of orders that have entered each status.",
		},
		[]string{"status"}, // "created" | "assigned" | "picked_up" | "delivered" | "failed"
	)

	kafkaPublishErrors := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kafka_publish_errors_total",
		Help: "Total Kafka publish failures (incremented only on hard errors, not DLQ writes).",
	})

	collectors := []prometheus.Collector{
		assignmentDuration,
		activeRiders,
		ordersTotal,
		kafkaPublishErrors,
	}
	for _, c := range collectors {
		if err := registry.Register(c); err != nil {
			return nil, fmt.Errorf("register metric: %w", err)
		}
	}

	return &Metrics{
		registry:           registry,
		assignmentDuration: assignmentDuration,
		activeRiders:       activeRiders,
		ordersTotal:        ordersTotal,
		kafkaPublishErrors: kafkaPublishErrors,
	}, nil
}

// RecordAssignment observes the duration of one assignment attempt.
// success=false should be passed when the operation returns any error,
// including ErrNoRiderAvailable.
func (m *Metrics) RecordAssignment(d time.Duration, success bool) {
	status := "success"
	if !success {
		status = "failure"
	}
	m.assignmentDuration.WithLabelValues(status).Observe(d.Seconds())
}

// IncActiveRiders increments the active-riders gauge by one.
// Call when a rider transitions to AVAILABLE.
func (m *Metrics) IncActiveRiders() { m.activeRiders.Inc() }

// DecActiveRiders decrements the active-riders gauge by one.
// Call when a rider transitions to BUSY or OFFLINE.
func (m *Metrics) DecActiveRiders() { m.activeRiders.Dec() }

// SetActiveRiders replaces the gauge with an absolute count.
// Use for periodic reconciliation against the database to correct drift.
func (m *Metrics) SetActiveRiders(n float64) { m.activeRiders.Set(n) }

// IncOrders increments the orders counter for the given status.
// domain.OrderStatus values ("CREATED", "PICKED_UP", …) are lowercased
// to match the Prometheus label convention.
func (m *Metrics) IncOrders(status domain.OrderStatus) {
	m.ordersTotal.WithLabelValues(strings.ToLower(string(status))).Inc()
}

// IncKafkaErrors increments the Kafka hard-failure counter.
// Do NOT call this for ErrPublishedToDLQ — that is a soft failure.
func (m *Metrics) IncKafkaErrors() { m.kafkaPublishErrors.Inc() }

// Handler returns the promhttp handler for the /metrics endpoint.
// Wire it to your mux: mux.Handle("GET /metrics", m.Handler())
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
