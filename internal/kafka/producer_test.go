package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	"rider-assignment-service/internal/domain"
)

// mockWriter captures messages and can be configured to fail N times.
// failN == -1 means always fail. err overrides the default failure error.
type mockWriter struct {
	mu       sync.Mutex
	messages []kafkago.Message
	failN    int
	calls    int
	err      error // if set, returned instead of the default error on failure
}

func (m *mockWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.failN == -1 || m.calls <= m.failN {
		if m.err != nil {
			return m.err
		}
		return errors.New("mock: kafka broker unavailable")
	}
	m.messages = append(m.messages, msgs...)
	return nil
}

func (m *mockWriter) Close() error { return nil }

func (m *mockWriter) captured() []kafkago.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]kafkago.Message, len(m.messages))
	copy(out, m.messages)
	return out
}

// --- helpers ---

func mustUnmarshalEvent(t *testing.T, data []byte) eventMessage {
	t.Helper()
	var e eventMessage
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal event message: %v", err)
	}
	return e
}

func mustUnmarshalDLQ(t *testing.T, data []byte) dlqMessage {
	t.Helper()
	var d dlqMessage
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("unmarshal dlq message: %v", err)
	}
	return d
}

// --- tests ---

func TestPublishOrderEvent_AllStatusEventTypes(t *testing.T) {
	tests := []struct {
		status    domain.OrderStatus
		wantEvent string
	}{
		{domain.StatusCreated, "ORDER_CREATED"},
		{domain.StatusAssigned, "ORDER_ASSIGNED"},
		{domain.StatusPickedUp, "ORDER_PICKED_UP"},
		{domain.StatusDelivered, "ORDER_DELIVERED"},
		{domain.StatusFailed, "ORDER_FAILED"},
		{domain.StatusCancelled, "ORDER_CANCELLED"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			w := &mockWriter{}
			dlq := &mockWriter{}
			p := newProducerWithWriters(w, dlq)

			err := p.PublishOrderEvent(context.Background(), "order-1", tt.status)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			msgs := w.captured()
			if len(msgs) != 1 {
				t.Fatalf("expected 1 message, got %d", len(msgs))
			}

			evt := mustUnmarshalEvent(t, msgs[0].Value)
			if evt.EventType != tt.wantEvent {
				t.Errorf("event_type = %q; want %q", evt.EventType, tt.wantEvent)
			}
		})
	}
}

func TestPublishOrderEvent_MessageShape(t *testing.T) {
	w := &mockWriter{}
	dlq := &mockWriter{}
	p := newProducerWithWriters(w, dlq)

	const orderID = "order-abc-123"

	if err := p.PublishOrderEvent(context.Background(), orderID, domain.StatusAssigned); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := w.captured()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	msg := msgs[0]

	// Key must equal orderID for partition affinity.
	if string(msg.Key) != orderID {
		t.Errorf("message key = %q; want %q", msg.Key, orderID)
	}

	evt := mustUnmarshalEvent(t, msg.Value)
	if evt.OrderID != orderID {
		t.Errorf("order_id = %q; want %q", evt.OrderID, orderID)
	}
	if evt.EventType != "ORDER_ASSIGNED" {
		t.Errorf("event_type = %q; want ORDER_ASSIGNED", evt.EventType)
	}
	if evt.Timestamp.IsZero() {
		t.Error("timestamp must not be zero")
	}
	if len(dlq.captured()) != 0 {
		t.Error("dlq must be empty on success")
	}
}

func TestPublishOrderEvent_UnknownStatus(t *testing.T) {
	w := &mockWriter{}
	dlq := &mockWriter{}
	p := newProducerWithWriters(w, dlq)

	err := p.PublishOrderEvent(context.Background(), "order-1", domain.OrderStatus("BOGUS"))
	if err == nil {
		t.Fatal("expected error for unknown status, got nil")
	}
	if len(w.captured()) != 0 {
		t.Error("no message should be written for an unknown status")
	}
}

func TestPublishOrderEvent_RetrySucceedsBeforeExhaustion(t *testing.T) {
	tests := []struct {
		name      string
		failFirst int // fail this many times then succeed
	}{
		{"fail once then succeed", 1},
		{"fail twice then succeed", 2},
		{"fail three times then succeed", maxRetries}, // last allowed retry
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &mockWriter{failN: tt.failFirst}
			dlq := &mockWriter{}
			p := newProducerWithWriters(w, dlq)

			err := p.PublishOrderEvent(context.Background(), "order-1", domain.StatusCreated)
			if err != nil {
				t.Fatalf("expected success after retries, got: %v", err)
			}

			w.mu.Lock()
			totalCalls := w.calls
			w.mu.Unlock()

			wantCalls := tt.failFirst + 1
			if totalCalls != wantCalls {
				t.Errorf("writer called %d times; want %d", totalCalls, wantCalls)
			}
			if len(dlq.captured()) != 0 {
				t.Error("dlq must be empty when retry eventually succeeds")
			}
		})
	}
}

func TestPublishOrderEvent_AllRetriesExhaustedSentToDLQ(t *testing.T) {
	w := &mockWriter{failN: -1} // always fail
	dlq := &mockWriter{}
	p := newProducerWithWriters(w, dlq)

	err := p.PublishOrderEvent(context.Background(), "order-1", domain.StatusCreated)
	if !errors.Is(err, ErrPublishedToDLQ) {
		t.Fatalf("expected ErrPublishedToDLQ, got: %v", err)
	}

	w.mu.Lock()
	totalCalls := w.calls
	w.mu.Unlock()

	// initial attempt + maxRetries
	wantCalls := 1 + maxRetries
	if totalCalls != wantCalls {
		t.Errorf("writer called %d times; want %d (1 initial + %d retries)", totalCalls, wantCalls, maxRetries)
	}

	dlqMsgs := dlq.captured()
	if len(dlqMsgs) != 1 {
		t.Fatalf("expected 1 dlq message, got %d", len(dlqMsgs))
	}
}

func TestPublishOrderEvent_DLQMessageContent(t *testing.T) {
	const orderID = "order-dlq-test"

	w := &mockWriter{failN: -1, err: errors.New("mock: write failed")}
	dlq := &mockWriter{}
	p := newProducerWithWriters(w, dlq)

	_ = p.PublishOrderEvent(context.Background(), orderID, domain.StatusAssigned)

	dlqMsgs := dlq.captured()
	if len(dlqMsgs) != 1 {
		t.Fatalf("expected 1 dlq message, got %d", len(dlqMsgs))
	}

	d := mustUnmarshalDLQ(t, dlqMsgs[0].Value)

	if d.ErrorReason != "mock: write failed" {
		t.Errorf("dlq error_reason = %q; want %q", d.ErrorReason, "mock: write failed")
	}
	if d.RetryCount != maxRetries {
		t.Errorf("dlq retry_count = %d; want %d", d.RetryCount, maxRetries)
	}
	if d.FirstFailureAt.IsZero() {
		t.Error("dlq first_failure_at must not be zero")
	}

	// Original payload must be a valid event message for the given order.
	orig := mustUnmarshalEvent(t, d.OriginalPayload)
	if orig.OrderID != orderID {
		t.Errorf("dlq original_payload.order_id = %q; want %q", orig.OrderID, orderID)
	}
	if orig.EventType != "ORDER_ASSIGNED" {
		t.Errorf("dlq original_payload.event_type = %q; want ORDER_ASSIGNED", orig.EventType)
	}
}

func TestPublishOrderEvent_DLQWriteFails(t *testing.T) {
	w := &mockWriter{failN: -1}
	dlq := &mockWriter{failN: -1} // DLQ also unavailable
	p := newProducerWithWriters(w, dlq)

	err := p.PublishOrderEvent(context.Background(), "order-1", domain.StatusCreated)
	if err == nil {
		t.Fatal("expected error when DLQ write also fails, got nil")
	}
	// Must NOT be ErrPublishedToDLQ — the message was NOT safely stored.
	if errors.Is(err, ErrPublishedToDLQ) {
		t.Error("should not return ErrPublishedToDLQ when DLQ write failed")
	}
}

func TestPublishOrderEvent_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before first write attempt completes

	w := &mockWriter{failN: -1}
	dlq := &mockWriter{}
	p := newProducerWithWriters(w, dlq)

	err := p.PublishOrderEvent(ctx, "order-1", domain.StatusCreated)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in error chain, got: %v", err)
	}
	// Context was cancelled mid-retry — message must NOT have gone to DLQ.
	if len(dlq.captured()) != 0 {
		t.Error("dlq must be empty when context is cancelled")
	}
}
