package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"rider-assignment-service/internal/domain"
)

const (
	geoKey            = "riders:geo"
	riderStatusKeyFmt = "rider:status:%s"
	// maxGeoCandidates caps the GEOSEARCH result set before status filtering.
	maxGeoCandidates = 50
)

var ErrNoRiderAvailable = errors.New("no rider available within radius")

// RedisRepository is the contract for all geospatial and availability operations.
type RedisRepository interface {
	AddRiderLocation(ctx context.Context, riderID string, lat, lng float64) error
	FindNearestRider(ctx context.Context, lat, lng, radiusKm float64) (riderID string, err error)
	SetRiderAvailable(ctx context.Context, riderID string, available bool) error
	// RemoveRiderLocation removes a rider from the geo index when they go OFFLINE.
	// Called in addition to SetRiderAvailable(false) by the handler that processes
	// OFFLINE transitions, because a bool alone cannot distinguish BUSY from OFFLINE.
	RemoveRiderLocation(ctx context.Context, riderID string) error
}

type redisRepository struct {
	client *redis.Client
}

func NewRedisRepository(client *redis.Client) RedisRepository {
	return &redisRepository{client: client}
}

// AddRiderLocation inserts or updates the rider's position in the geo sorted set.
// Longitude and latitude order follows the Redis convention (lng before lat).
func (r *redisRepository) AddRiderLocation(ctx context.Context, riderID string, lat, lng float64) error {
	if err := r.client.GeoAdd(ctx, geoKey, &redis.GeoLocation{
		Name:      riderID,
		Longitude: lng,
		Latitude:  lat,
	}).Err(); err != nil {
		return fmt.Errorf("add rider location %s: %w", riderID, err)
	}
	return nil
}

// FindNearestRider runs GEOSEARCH to collect candidates within the radius, then
// pipelines a GET for each rider's status key and returns the nearest AVAILABLE one.
// Riders in the geo index may be AVAILABLE or BUSY; OFFLINE riders are absent
// (removed via RemoveRiderLocation when they go offline).
func (r *redisRepository) FindNearestRider(ctx context.Context, lat, lng, radiusKm float64) (string, error) {
	candidates, err := r.client.GeoSearch(ctx, geoKey, &redis.GeoSearchQuery{
		Longitude:  lng,
		Latitude:   lat,
		Radius:     radiusKm,
		RadiusUnit: "km",
		Sort:       "ASC",
		Count:      maxGeoCandidates,
	}).Result()
	if err != nil {
		return "", fmt.Errorf("geosearch riders: %w", err)
	}
	if len(candidates) == 0 {
		return "", ErrNoRiderAvailable
	}

	// Pipeline all status lookups in a single round trip.
	pipe := r.client.Pipeline()
	statusCmds := make([]*redis.StringCmd, len(candidates))
	for i, id := range candidates {
		statusCmds[i] = pipe.Get(ctx, fmt.Sprintf(riderStatusKeyFmt, id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("pipeline rider status lookup: %w", err)
	}

	for i, cmd := range statusCmds {
		status, err := cmd.Result()
		if err != nil {
			continue // missing key or pipeline error — treat as unavailable
		}
		if domain.RiderStatus(status) == domain.RiderStatusAvailable {
			return candidates[i], nil
		}
	}

	return "", ErrNoRiderAvailable
}

// SetRiderAvailable sets the rider's status key to AVAILABLE (true) or BUSY (false).
// It does not modify the geo index — OFFLINE removal is handled by RemoveRiderLocation.
func (r *redisRepository) SetRiderAvailable(ctx context.Context, riderID string, available bool) error {
	status := domain.RiderStatusBusy
	if available {
		status = domain.RiderStatusAvailable
	}
	key := fmt.Sprintf(riderStatusKeyFmt, riderID)
	if err := r.client.Set(ctx, key, string(status), 0).Err(); err != nil {
		return fmt.Errorf("set rider status %s: %w", riderID, err)
	}
	return nil
}

// RemoveRiderLocation evicts the rider from the geo sorted set (ZREM).
// Must be called when a rider transitions to OFFLINE so they are excluded
// from future GEOSEARCH results without waiting for a TTL.
func (r *redisRepository) RemoveRiderLocation(ctx context.Context, riderID string) error {
	if err := r.client.ZRem(ctx, geoKey, riderID).Err(); err != nil {
		return fmt.Errorf("remove rider location %s: %w", riderID, err)
	}
	return nil
}
