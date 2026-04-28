package domain

import (
	"errors"
	"testing"
)

func TestOrderTransition(t *testing.T) {
	tests := []struct {
		name    string
		initial OrderStatus
		target  OrderStatus
		wantErr bool
	}{
		// --- valid transitions ---
		{name: "created → assigned", initial: StatusCreated, target: StatusAssigned, wantErr: false},
		{name: "created → cancelled", initial: StatusCreated, target: StatusCancelled, wantErr: false},
		{name: "assigned → picked_up", initial: StatusAssigned, target: StatusPickedUp, wantErr: false},
		{name: "assigned → cancelled", initial: StatusAssigned, target: StatusCancelled, wantErr: false},
		{name: "picked_up → delivered", initial: StatusPickedUp, target: StatusDelivered, wantErr: false},
		{name: "picked_up → failed", initial: StatusPickedUp, target: StatusFailed, wantErr: false},

		// --- invalid transitions: skipping states forward ---
		{name: "created → picked_up (skip)", initial: StatusCreated, target: StatusPickedUp, wantErr: true},
		{name: "created → delivered (skip)", initial: StatusCreated, target: StatusDelivered, wantErr: true},
		{name: "created → failed (skip)", initial: StatusCreated, target: StatusFailed, wantErr: true},
		{name: "assigned → delivered (skip)", initial: StatusAssigned, target: StatusDelivered, wantErr: true},
		{name: "assigned → failed (skip)", initial: StatusAssigned, target: StatusFailed, wantErr: true},

		// --- invalid transitions: backwards ---
		{name: "assigned → created (backward)", initial: StatusAssigned, target: StatusCreated, wantErr: true},
		{name: "picked_up → created (backward)", initial: StatusPickedUp, target: StatusCreated, wantErr: true},
		{name: "picked_up → assigned (backward)", initial: StatusPickedUp, target: StatusAssigned, wantErr: true},

		// --- invalid transitions: cancellation not allowed after pickup ---
		{name: "picked_up → cancelled (not permitted)", initial: StatusPickedUp, target: StatusCancelled, wantErr: true},

		// --- invalid transitions: self-transitions ---
		{name: "created → created (self)", initial: StatusCreated, target: StatusCreated, wantErr: true},
		{name: "assigned → assigned (self)", initial: StatusAssigned, target: StatusAssigned, wantErr: true},

		// --- terminal states: no transitions allowed ---
		{name: "delivered → created (terminal)", initial: StatusDelivered, target: StatusCreated, wantErr: true},
		{name: "delivered → cancelled (terminal)", initial: StatusDelivered, target: StatusCancelled, wantErr: true},
		{name: "failed → assigned (terminal)", initial: StatusFailed, target: StatusAssigned, wantErr: true},
		{name: "failed → delivered (terminal)", initial: StatusFailed, target: StatusDelivered, wantErr: true},
		{name: "cancelled → created (terminal)", initial: StatusCancelled, target: StatusCreated, wantErr: true},
		{name: "cancelled → assigned (terminal)", initial: StatusCancelled, target: StatusAssigned, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Order{Status: tt.initial}

			err := o.Transition(tt.target)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Transition() expected error, got nil")
				}
				if !errors.Is(err, ErrInvalidTransition) {
					t.Errorf("Transition() error = %v; want chain containing ErrInvalidTransition", err)
				}
				// State must not change on error.
				if o.Status != tt.initial {
					t.Errorf("Transition() mutated status to %q on error; want %q", o.Status, tt.initial)
				}
				return
			}

			if err != nil {
				t.Fatalf("Transition() unexpected error: %v", err)
			}
			if o.Status != tt.target {
				t.Errorf("Transition() status = %q; want %q", o.Status, tt.target)
			}
		})
	}
}
