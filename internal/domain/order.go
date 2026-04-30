package domain

import (
	"errors"
	"fmt"
	"time"
)

type OrderStatus string

const (
	StatusCreated   OrderStatus = "CREATED"
	StatusAssigned  OrderStatus = "ASSIGNED"
	StatusPickedUp  OrderStatus = "PICKED_UP"
	StatusDelivered OrderStatus = "DELIVERED"
	StatusFailed    OrderStatus = "FAILED"
	StatusCancelled OrderStatus = "CANCELLED"
)

// ErrInvalidTransition is returned when a state transition is not permitted
// by the order state machine.
var ErrInvalidTransition = errors.New("invalid state transition")

// validTransitions defines the only permitted state changes.
// Terminal states (DELIVERED, FAILED, CANCELLED) are absent — any attempt
// to transition from them falls into the !ok branch of Transition.
var validTransitions = map[OrderStatus][]OrderStatus{
	StatusCreated:  {StatusAssigned, StatusCancelled},
	StatusAssigned: {StatusPickedUp, StatusCancelled},
	StatusPickedUp: {StatusDelivered, StatusFailed},
}

// Order represents a delivery order moving through its lifecycle.
// RiderID is nil until the order reaches StatusAssigned.
type Order struct {
	ID        string      `json:"id"`
	RiderID   *string     `json:"rider_id,omitempty"`
	Status    OrderStatus `json:"status"`
	PickupLat float64     `json:"pickup_lat"`
	PickupLng float64     `json:"pickup_lng"`
	CreatedAt time.Time   `json:"created_at"`
}

// Transition attempts to advance the order to the given status.
// It mutates o.Status only on success; on failure the order is unchanged
// and the error wraps ErrInvalidTransition so callers can use errors.Is.
func (o *Order) Transition(to OrderStatus) error {
	allowed, ok := validTransitions[o.Status]
	if !ok {
		return fmt.Errorf("transition from %s to %s: %w", o.Status, to, ErrInvalidTransition)
	}
	for _, s := range allowed {
		if s == to {
			o.Status = to
			return nil
		}
	}
	return fmt.Errorf("transition from %s to %s: %w", o.Status, to, ErrInvalidTransition)
}
