package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"rider-assignment-service/internal/domain"
	"rider-assignment-service/internal/kafka"
	"rider-assignment-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockOrderRepo struct {
	// canned responses
	order     *domain.Order
	getErr    error
	createErr error
	updateErr error
	assignErr error

	// captured calls
	created       *domain.Order
	updatedID     string
	updatedStatus domain.OrderStatus
	assignedOrder string
	assignedRider string
}

func (m *mockOrderRepo) Create(_ context.Context, o *domain.Order) error {
	m.created = o
	return m.createErr
}
func (m *mockOrderRepo) GetByID(_ context.Context, _ string) (*domain.Order, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.order, nil
}
func (m *mockOrderRepo) UpdateStatus(_ context.Context, id string, s domain.OrderStatus) error {
	m.updatedID = id
	m.updatedStatus = s
	return m.updateErr
}
func (m *mockOrderRepo) AssignRider(_ context.Context, orderID, riderID string) error {
	m.assignedOrder = orderID
	m.assignedRider = riderID
	return m.assignErr
}

// ---------------------------------------------------------------------------

type mockRiderRepo struct {
	markAssignedErr error
	updateAvailErr  error

	markedRiderID string
}

func (m *mockRiderRepo) Create(_ context.Context, _ *domain.Rider) error                { return nil }
func (m *mockRiderRepo) UpdateLocation(_ context.Context, _ string, _, _ float64) error { return nil }
func (m *mockRiderRepo) GetByID(_ context.Context, _ string) (*domain.Rider, error) {
	return nil, nil
}
func (m *mockRiderRepo) UpdateAvailability(_ context.Context, _ string, _ bool) error {
	return m.updateAvailErr
}
func (m *mockRiderRepo) MarkAssigned(_ context.Context, id string) error {
	m.markedRiderID = id
	return m.markAssignedErr
}

// ---------------------------------------------------------------------------

type mockRedisRepo struct {
	nearestRiderID string // "" → ErrNoRiderAvailable
	findErr        error  // overrides nearestRiderID behaviour when set
	setAvailErr    error

	setAvailCalls int
}

func (m *mockRedisRepo) FindNearestRider(_ context.Context, _, _, _ float64) (string, error) {
	if m.findErr != nil {
		return "", m.findErr
	}
	if m.nearestRiderID == "" {
		return "", repository.ErrNoRiderAvailable
	}
	return m.nearestRiderID, nil
}
func (m *mockRedisRepo) SetRiderAvailable(_ context.Context, _ string, _ bool) error {
	m.setAvailCalls++
	return m.setAvailErr
}
func (m *mockRedisRepo) AddRiderLocation(_ context.Context, _ string, _, _ float64) error { return nil }
func (m *mockRedisRepo) RemoveRiderLocation(_ context.Context, _ string) error            { return nil }

// ---------------------------------------------------------------------------

type capturedEvent struct {
	orderID string
	status  domain.OrderStatus
}

type mockKafka struct {
	events []capturedEvent
	err    error
}

func (m *mockKafka) PublishOrderEvent(_ context.Context, orderID string, status domain.OrderStatus) error {
	m.events = append(m.events, capturedEvent{orderID: orderID, status: status})
	return m.err
}
func (m *mockKafka) Close() error { return nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newService(or *mockOrderRepo, rr *mockRiderRepo, red *mockRedisRepo, kf *mockKafka) AssignmentService {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewAssignmentService(or, rr, red, kf, log)
}

func orderInStatus(status domain.OrderStatus) *domain.Order {
	return &domain.Order{ID: "order-1", Status: status, PickupLat: 1.23, PickupLng: 4.56}
}

// ---------------------------------------------------------------------------
// TestAssignRider
// ---------------------------------------------------------------------------

func TestAssignRider(t *testing.T) {
	const riderID = "rider-42"

	tests := []struct {
		name string

		// repo setup
		order         *domain.Order
		getErr        error
		assignErr     error
		markAssignErr error
		setAvailErr   error

		// redis setup
		nearestRider string // "" → no rider available
		findErr      error

		// kafka
		kafkaErr error

		// expectations
		wantErr        bool
		wantErrIs      error
		wantStatus     domain.OrderStatus
		wantRiderID    string
		wantKafkaCount int
	}{
		{
			name:           "happy path — nearest rider assigned",
			order:          orderInStatus(domain.StatusCreated),
			nearestRider:   riderID,
			wantStatus:     domain.StatusAssigned,
			wantRiderID:    riderID,
			wantKafkaCount: 1,
		},
		{
			name:      "order not found",
			getErr:    fmt.Errorf("get order: %w", repository.ErrOrderNotFound),
			wantErr:   true,
			wantErrIs: repository.ErrOrderNotFound,
		},
		{
			name:      "order already assigned — invalid state transition",
			order:     orderInStatus(domain.StatusAssigned),
			wantErr:   true,
			wantErrIs: domain.ErrInvalidTransition,
		},
		{
			name:      "order picked up — invalid state transition",
			order:     orderInStatus(domain.StatusPickedUp),
			wantErr:   true,
			wantErrIs: domain.ErrInvalidTransition,
		},
		{
			name:      "no rider available within 5 km",
			order:     orderInStatus(domain.StatusCreated),
			wantErr:   true,
			wantErrIs: repository.ErrNoRiderAvailable,
		},
		{
			name:    "geosearch infrastructure error",
			order:   orderInStatus(domain.StatusCreated),
			findErr: errors.New("redis: connection refused"),
			wantErr: true,
		},
		{
			name:         "db assignment write fails",
			order:        orderInStatus(domain.StatusCreated),
			nearestRider: riderID,
			assignErr:    errors.New("postgres: deadlock"),
			wantErr:      true,
		},
		{
			name:          "mark rider assigned in db fails",
			order:         orderInStatus(domain.StatusCreated),
			nearestRider:  riderID,
			markAssignErr: errors.New("postgres: connection lost"),
			wantErr:       true,
		},
		{
			name:         "set rider unavailable in redis fails",
			order:        orderInStatus(domain.StatusCreated),
			nearestRider: riderID,
			setAvailErr:  errors.New("redis: write timeout"),
			wantErr:      true,
		},
		{
			name:           "kafka publish fails — operation still succeeds",
			order:          orderInStatus(domain.StatusCreated),
			nearestRider:   riderID,
			kafkaErr:       errors.New("kafka: broker unavailable"),
			wantStatus:     domain.StatusAssigned,
			wantRiderID:    riderID,
			wantKafkaCount: 1, // attempted even though it fails
		},
		{
			name:           "kafka sent to dlq — operation still succeeds",
			order:          orderInStatus(domain.StatusCreated),
			nearestRider:   riderID,
			kafkaErr:       kafka.ErrPublishedToDLQ,
			wantStatus:     domain.StatusAssigned,
			wantRiderID:    riderID,
			wantKafkaCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			or := &mockOrderRepo{
				order:     tt.order,
				getErr:    tt.getErr,
				assignErr: tt.assignErr,
			}
			rr := &mockRiderRepo{markAssignedErr: tt.markAssignErr}
			red := &mockRedisRepo{
				nearestRiderID: tt.nearestRider,
				findErr:        tt.findErr,
				setAvailErr:    tt.setAvailErr,
			}
			kf := &mockKafka{err: tt.kafkaErr}

			svc := newService(or, rr, red, kf)
			got, err := svc.AssignRider(context.Background(), "order-1")

			if tt.wantErr {
				if err == nil {
					t.Fatalf("AssignRider() expected error, got nil")
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Errorf("AssignRider() error chain = %v; want errors.Is(%T)", err, tt.wantErrIs)
				}
				if got != nil {
					t.Errorf("AssignRider() expected nil order on error, got %+v", got)
				}
				if len(kf.events) != 0 {
					t.Errorf("AssignRider() Kafka must not be called on error, got %d calls", len(kf.events))
				}
				return
			}

			if err != nil {
				t.Fatalf("AssignRider() unexpected error: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("order.Status = %q; want %q", got.Status, tt.wantStatus)
			}
			if tt.wantRiderID != "" {
				if got.RiderID == nil || *got.RiderID != tt.wantRiderID {
					t.Errorf("order.RiderID = %v; want %q", got.RiderID, tt.wantRiderID)
				}
			}
			if len(kf.events) != tt.wantKafkaCount {
				t.Errorf("Kafka event count = %d; want %d", len(kf.events), tt.wantKafkaCount)
			}
			if tt.wantKafkaCount > 0 && kf.events[0].status != domain.StatusAssigned {
				t.Errorf("Kafka event status = %q; want %q", kf.events[0].status, domain.StatusAssigned)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCreateOrder
// ---------------------------------------------------------------------------

func TestCreateOrder(t *testing.T) {
	tests := []struct {
		name      string
		createErr error
		wantErr   bool
	}{
		{name: "happy path", wantErr: false},
		{name: "db write fails", createErr: errors.New("postgres: disk full"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			or := &mockOrderRepo{createErr: tt.createErr}
			kf := &mockKafka{}
			svc := newService(or, &mockRiderRepo{}, &mockRedisRepo{}, kf)

			got, err := svc.CreateOrder(context.Background(), 12.34, 56.78)

			if tt.wantErr {
				if err == nil {
					t.Fatal("CreateOrder() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateOrder() unexpected error: %v", err)
			}
			if got.ID == "" {
				t.Error("CreateOrder() order ID must not be empty")
			}
			if got.Status != domain.StatusCreated {
				t.Errorf("CreateOrder() status = %q; want CREATED", got.Status)
			}
			if got.PickupLat != 12.34 || got.PickupLng != 56.78 {
				t.Errorf("CreateOrder() coordinates = (%v, %v); want (12.34, 56.78)", got.PickupLat, got.PickupLng)
			}
			if got.CreatedAt.IsZero() {
				t.Error("CreateOrder() CreatedAt must not be zero")
			}
			// Kafka must be attempted even if it eventually fails or goes to DLQ.
			if len(kf.events) != 1 {
				t.Errorf("Kafka event count = %d; want 1", len(kf.events))
			}
			if kf.events[0].status != domain.StatusCreated {
				t.Errorf("Kafka event status = %q; want ORDER_CREATED", kf.events[0].status)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestUpdateOrderStatus
// ---------------------------------------------------------------------------

func TestUpdateOrderStatus(t *testing.T) {
	tests := []struct {
		name       string
		initial    domain.OrderStatus
		target     domain.OrderStatus
		updateErr  error
		getErr     error
		wantErr    bool
		wantErrIs  error
		wantStatus domain.OrderStatus
	}{
		{
			name:       "assigned → picked_up",
			initial:    domain.StatusAssigned,
			target:     domain.StatusPickedUp,
			wantStatus: domain.StatusPickedUp,
		},
		{
			name:       "picked_up → delivered",
			initial:    domain.StatusPickedUp,
			target:     domain.StatusDelivered,
			wantStatus: domain.StatusDelivered,
		},
		{
			name:       "assigned → cancelled",
			initial:    domain.StatusAssigned,
			target:     domain.StatusCancelled,
			wantStatus: domain.StatusCancelled,
		},
		{
			name:      "invalid transition — created to delivered",
			initial:   domain.StatusCreated,
			target:    domain.StatusDelivered,
			wantErr:   true,
			wantErrIs: domain.ErrInvalidTransition,
		},
		{
			name:      "invalid transition — terminal state delivered",
			initial:   domain.StatusDelivered,
			target:    domain.StatusCancelled,
			wantErr:   true,
			wantErrIs: domain.ErrInvalidTransition,
		},
		{
			name:      "order not found",
			initial:   domain.StatusCreated,
			target:    domain.StatusAssigned,
			getErr:    fmt.Errorf("get order: %w", repository.ErrOrderNotFound),
			wantErr:   true,
			wantErrIs: repository.ErrOrderNotFound,
		},
		{
			name:      "db persist fails",
			initial:   domain.StatusAssigned,
			target:    domain.StatusPickedUp,
			updateErr: errors.New("postgres: connection reset"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			or := &mockOrderRepo{
				order:     orderInStatus(tt.initial),
				getErr:    tt.getErr,
				updateErr: tt.updateErr,
			}
			kf := &mockKafka{}
			svc := newService(or, &mockRiderRepo{}, &mockRedisRepo{}, kf)

			got, err := svc.UpdateOrderStatus(context.Background(), "order-1", tt.target)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("UpdateOrderStatus() expected error, got nil")
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Errorf("UpdateOrderStatus() error chain = %v; want errors.Is(%T)", err, tt.wantErrIs)
				}
				if len(kf.events) != 0 {
					t.Errorf("Kafka must not be called on error, got %d calls", len(kf.events))
				}
				return
			}

			if err != nil {
				t.Fatalf("UpdateOrderStatus() unexpected error: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("order.Status = %q; want %q", got.Status, tt.wantStatus)
			}
			if len(kf.events) != 1 {
				t.Errorf("Kafka event count = %d; want 1", len(kf.events))
			}
			if kf.events[0].status != tt.target {
				t.Errorf("Kafka event status = %q; want %q", kf.events[0].status, tt.target)
			}
		})
	}
}
