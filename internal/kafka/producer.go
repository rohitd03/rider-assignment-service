package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"rider-assignment-service/internal/domain"
)

const (
	topicOrderEvents = "order-events"
	topicDLQ         = "order-events.dlq"
	maxRetries       = 3
	defaultBackoff   = 100 * time.Millisecond
)

// ErrPublishedToDLQ signals that all retries were exhausted and the message
// was safely written to the dead letter queue. The caller should log and
// increment a metric but need not fail the originating operation.
var ErrPublishedToDLQ = errors.New("message published to dead letter queue after retries")

var statusToEventType = map[domain.OrderStatus]string{
	domain.StatusCreated:   "ORDER_CREATED",
	domain.StatusAssigned:  "ORDER_ASSIGNED",
	domain.StatusPickedUp:  "ORDER_PICKED_UP",
	domain.StatusDelivered: "ORDER_DELIVERED",
	domain.StatusFailed:    "ORDER_FAILED",
	domain.StatusCancelled: "ORDER_CANCELLED",
}

type eventMessage struct {
	OrderID   string    `json:"order_id"`
	EventType string    `json:"event_type"`
	Timestamp time.Time `json:"timestamp"`
}

type dlqMessage struct {
	OriginalPayload json.RawMessage `json:"original_payload"`
	ErrorReason     string          `json:"error_reason"`
	RetryCount      int             `json:"retry_count"`
	FirstFailureAt  time.Time       `json:"first_failure_at"`
}

// messageWriter is an abstraction over kafkago.Writer for test injection.
type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// KafkaProducer publishes order lifecycle events to Kafka.
type KafkaProducer interface {
	PublishOrderEvent(ctx context.Context, orderID string, status domain.OrderStatus) error
	Close() error
}

type producer struct {
	writer      messageWriter
	dlqWriter   messageWriter
	backoffBase time.Duration
}

func NewKafkaProducer(brokerAddr string) KafkaProducer {
	return &producer{
		writer: &kafkago.Writer{
			Addr:         kafkago.TCP(brokerAddr),
			Topic:        topicOrderEvents,
			Balancer:     &kafkago.LeastBytes{},
			RequiredAcks: kafkago.RequireAll,
		},
		dlqWriter: &kafkago.Writer{
			Addr:         kafkago.TCP(brokerAddr),
			Topic:        topicDLQ,
			Balancer:     &kafkago.LeastBytes{},
			RequiredAcks: kafkago.RequireAll,
		},
		backoffBase: defaultBackoff,
	}
}

// newProducerWithWriters constructs a producer with injected writers.
// Only used within this package (tests).
func newProducerWithWriters(writer, dlqWriter messageWriter) KafkaProducer {
	return &producer{
		writer:      writer,
		dlqWriter:   dlqWriter,
		backoffBase: time.Millisecond, // fast retries in tests
	}
}

func (p *producer) PublishOrderEvent(ctx context.Context, orderID string, status domain.OrderStatus) error {
	eventType, ok := statusToEventType[status]
	if !ok {
		return fmt.Errorf("publish order event: unknown status %q", status)
	}

	payload := eventMessage{
		OrderID:   orderID,
		EventType: eventType,
		Timestamp: time.Now().UTC(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("publish order event: marshal payload: %w", err)
	}

	return p.publishWithRetry(ctx, kafkago.Message{
		Key:   []byte(orderID),
		Value: data,
	}, data)
}

func (p *producer) publishWithRetry(ctx context.Context, msg kafkago.Message, payload []byte) error {
	var (
		firstFailureAt time.Time
		lastErr        error
	)

	backoff := p.backoffBase
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = p.writer.WriteMessages(ctx, msg)
		if lastErr == nil {
			return nil
		}
		if attempt == 0 {
			firstFailureAt = time.Now().UTC()
		}
		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				return fmt.Errorf("publish aborted after %d attempt(s): %w", attempt+1, ctx.Err())
			case <-time.After(backoff):
				backoff *= 2
			}
		}
	}

	return p.writeToDLQ(ctx, payload, lastErr, maxRetries, firstFailureAt)
}

func (p *producer) writeToDLQ(ctx context.Context, originalPayload []byte, publishErr error, retryCount int, firstFailureAt time.Time) error {
	dlq := dlqMessage{
		OriginalPayload: json.RawMessage(originalPayload),
		ErrorReason:     publishErr.Error(),
		RetryCount:      retryCount,
		FirstFailureAt:  firstFailureAt,
	}
	data, err := json.Marshal(dlq)
	if err != nil {
		return fmt.Errorf("marshal dlq message: %w", err)
	}
	if err := p.dlqWriter.WriteMessages(ctx, kafkago.Message{Value: data}); err != nil {
		return fmt.Errorf("write to dlq (original err: %v): %w", publishErr, err)
	}
	return ErrPublishedToDLQ
}

func (p *producer) Close() error {
	if err := p.writer.Close(); err != nil {
		return fmt.Errorf("close order-events writer: %w", err)
	}
	if err := p.dlqWriter.Close(); err != nil {
		return fmt.Errorf("close dlq writer: %w", err)
	}
	return nil
}
