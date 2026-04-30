package domain

import "time"

// RiderStatus represents the three operating states a rider can be in.
// AVAILABLE and BUSY riders remain in the Redis geo index.
// OFFLINE riders are removed from the geo index entirely.
type RiderStatus string

const (
	RiderStatusAvailable RiderStatus = "AVAILABLE"
	RiderStatusBusy      RiderStatus = "BUSY"
	RiderStatusOffline   RiderStatus = "OFFLINE"
)

// Rider represents a delivery rider and their current position.
// IsAvailable reflects whether the rider can accept a new order
// (i.e. status is AVAILABLE). LastAssignedAt drives tie-breaking:
// the rider idle the longest is preferred when multiple riders are equidistant.
type Rider struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	IsAvailable    bool      `json:"is_available"`
	Lat            float64   `json:"lat"`
	Lng            float64   `json:"lng"`
	LastAssignedAt time.Time `json:"last_assigned_at"`
}
