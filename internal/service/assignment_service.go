package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"rider-assignment-service/internal/domain"
	"rider-assignment-service/internal/kafka"
	"rider-assignment-service/internal/repository"
)

const searchRadiusKm = 5.0

// AssignmentService is the core business logic contract for order lifecycle management.
type AssignmentService interface {
	CreateOrder(ctx context.Context, pickupLat, pickupLng float64) (*domain.Order, error)
	GetOrder(ctx context.Context, id string) (*domain.Order, error)
	AssignRider(ctx context.Context, orderID string) (*domain.Order, error)
	UpdateOrderStatus(ctx context.Context, orderID string, status domain.OrderStatus) (*domain.Order, error)
}

type assignmentService struct {
	orderRepo repository.OrderRepository
	riderRepo repository.RiderRepository
	redisRepo repository.RedisRepository
	kafka     kafka.KafkaProducer
	log       *slog.Logger
}

func NewAssignmentService(
	orderRepo repository.OrderRepository,
	riderRepo repository.RiderRepository,
	redisRepo repository.RedisRepository,
	kafkaProducer kafka.KafkaProducer,
	log *slog.Logger,
) AssignmentService {
	return &assignmentService{
		orderRepo: orderRepo,
		riderRepo: riderRepo,
		redisRepo: redisRepo,
		kafka:     kafkaProducer,
		log:       log,
	}
}

// publishEvent emits a Kafka event and logs any failure rather than silently
// discarding it. ErrPublishedToDLQ is a Warn (event preserved in DLQ);
// any other error is an Error (event may be permanently lost).
func (s *assignmentService) publishEvent(ctx context.Context, orderID string, status domain.OrderStatus) {
	if err := s.kafka.PublishOrderEvent(ctx, orderID, status); err != nil {
		if errors.Is(err, kafka.ErrPublishedToDLQ) {
			s.log.WarnContext(ctx, "kafka event sent to dead letter queue",
				"order_id", orderID,
				"status", status,
			)
		} else {
			s.log.ErrorContext(ctx, "kafka publish failed — event may be lost",
				"order_id", orderID,
				"status", status,
				"error", err,
			)
		}
	}
}

func (s *assignmentService) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	order, err := s.orderRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	return order, nil
}

// CreateOrder persists a new order in CREATED status and emits an ORDER_CREATED event.
func (s *assignmentService) CreateOrder(ctx context.Context, pickupLat, pickupLng float64) (*domain.Order, error) {
	order := &domain.Order{
		ID:        uuid.New().String(),
		Status:    domain.StatusCreated,
		PickupLat: pickupLat,
		PickupLng: pickupLng,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.orderRepo.Create(ctx, order); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	s.publishEvent(ctx, order.ID, domain.StatusCreated)
	return order, nil
}

// AssignRider finds the nearest available rider within 5 km of the order's
// pickup location and atomically assigns them.
//
// Steps that mutate state are ordered so the most recoverable failure comes
// first: the PostgreSQL write is the source of truth, Redis is the cache.
// If Redis fails after the DB write the system is inconsistent but recoverable
// on the next rider location update; this is an acceptable trade-off without
// a distributed transaction.
func (s *assignmentService) AssignRider(ctx context.Context, orderID string) (*domain.Order, error) {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("assign rider: %w", err)
	}

	// Validate the transition in the domain layer before touching any store.
	if order.Status != domain.StatusCreated {
		return nil, fmt.Errorf("assign rider: order %s has status %s: %w",
			orderID, order.Status, domain.ErrInvalidTransition)
	}

	riderID, err := s.redisRepo.FindNearestRider(ctx, order.PickupLat, order.PickupLng, searchRadiusKm)
	if err != nil {
		if errors.Is(err, repository.ErrNoRiderAvailable) {
			return nil, repository.ErrNoRiderAvailable
		}
		return nil, fmt.Errorf("assign rider: find nearest rider: %w", err)
	}

	// Atomic DB write: sets status=ASSIGNED and rider_id together.
	// The WHERE status='CREATED' guard prevents a concurrent request from
	// double-assigning the same order if two calls race past the status check above.
	if err := s.orderRepo.AssignRider(ctx, orderID, riderID); err != nil {
		return nil, fmt.Errorf("assign rider: persist assignment: %w", err)
	}

	// Mark rider busy in PostgreSQL (updates last_assigned_at for future tie-breaking).
	if err := s.riderRepo.MarkAssigned(ctx, riderID); err != nil {
		return nil, fmt.Errorf("assign rider: mark rider as assigned: %w", err)
	}

	// Mark rider BUSY in Redis so they are skipped by concurrent GEOSEARCH calls.
	if err := s.redisRepo.SetRiderAvailable(ctx, riderID, false); err != nil {
		return nil, fmt.Errorf("assign rider: set rider unavailable in cache: %w", err)
	}

	s.publishEvent(ctx, orderID, domain.StatusAssigned)

	order.Status = domain.StatusAssigned
	order.RiderID = &riderID
	return order, nil
}

// UpdateOrderStatus validates the requested transition via the domain state
// machine, persists it, and emits the corresponding Kafka event.
func (s *assignmentService) UpdateOrderStatus(ctx context.Context, orderID string, status domain.OrderStatus) (*domain.Order, error) {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("update order status: %w", err)
	}
	// Transition mutates order.Status only on success.
	if err := order.Transition(status); err != nil {
		return nil, fmt.Errorf("update order status: %w", err)
	}
	if err := s.orderRepo.UpdateStatus(ctx, orderID, status); err != nil {
		return nil, fmt.Errorf("update order status: persist: %w", err)
	}
	s.publishEvent(ctx, orderID, status)
	return order, nil
}
