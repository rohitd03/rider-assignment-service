package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"rider-assignment-service/internal/domain"
	"rider-assignment-service/internal/repository"
)

// RiderService manages rider registration, location, and availability.
type RiderService interface {
	RegisterRider(ctx context.Context, name string, lat, lng float64) (*domain.Rider, error)
	UpdateRiderLocation(ctx context.Context, riderID string, lat, lng float64) error
	SetRiderAvailability(ctx context.Context, riderID string, available bool) error
}

type riderService struct {
	riderRepo repository.RiderRepository
	redisRepo repository.RedisRepository
	log       *slog.Logger
}

func NewRiderService(
	riderRepo repository.RiderRepository,
	redisRepo repository.RedisRepository,
	log *slog.Logger,
) RiderService {
	return &riderService{riderRepo: riderRepo, redisRepo: redisRepo, log: log}
}

// RegisterRider creates the rider in PostgreSQL, adds them to the Redis geo
// index, and marks them AVAILABLE so they are immediately assignable.
func (s *riderService) RegisterRider(ctx context.Context, name string, lat, lng float64) (*domain.Rider, error) {
	rider := &domain.Rider{
		ID:             uuid.New().String(),
		Name:           name,
		IsAvailable:    true,
		Lat:            lat,
		Lng:            lng,
		LastAssignedAt: time.Time{}, // zero — treated as longest idle for first assignment
	}
	if err := s.riderRepo.Create(ctx, rider); err != nil {
		return nil, fmt.Errorf("register rider: %w", err)
	}
	if err := s.redisRepo.AddRiderLocation(ctx, rider.ID, lat, lng); err != nil {
		return nil, fmt.Errorf("register rider: add to geo index: %w", err)
	}
	if err := s.redisRepo.SetRiderAvailable(ctx, rider.ID, true); err != nil {
		return nil, fmt.Errorf("register rider: set redis status: %w", err)
	}
	return rider, nil
}

// UpdateRiderLocation persists the new coordinates in PostgreSQL and refreshes
// the rider's position in the Redis geo index.
func (s *riderService) UpdateRiderLocation(ctx context.Context, riderID string, lat, lng float64) error {
	if err := s.riderRepo.UpdateLocation(ctx, riderID, lat, lng); err != nil {
		return fmt.Errorf("update rider location: %w", err)
	}
	if err := s.redisRepo.AddRiderLocation(ctx, riderID, lat, lng); err != nil {
		return fmt.Errorf("update rider location: refresh geo index: %w", err)
	}
	return nil
}

// SetRiderAvailability handles the ONLINE/OFFLINE lifecycle:
//   - available=true  → re-add rider to geo index using stored coordinates,
//     set Redis status to AVAILABLE.
//   - available=false → remove from geo index (ZREM) so GEOSEARCH never returns
//     them; PostgreSQL is updated for durability.
func (s *riderService) SetRiderAvailability(ctx context.Context, riderID string, available bool) error {
	if available {
		rider, err := s.riderRepo.GetByID(ctx, riderID)
		if err != nil {
			return fmt.Errorf("set rider availability: %w", err)
		}
		if err := s.redisRepo.AddRiderLocation(ctx, riderID, rider.Lat, rider.Lng); err != nil {
			return fmt.Errorf("set rider availability: add to geo index: %w", err)
		}
		if err := s.redisRepo.SetRiderAvailable(ctx, riderID, true); err != nil {
			return fmt.Errorf("set rider availability: set redis status: %w", err)
		}
	} else {
		// OFFLINE: remove from geo index so no new orders are assigned.
		if err := s.redisRepo.RemoveRiderLocation(ctx, riderID); err != nil {
			s.log.WarnContext(ctx, "failed to remove rider from geo index",
				"rider_id", riderID, "error", err)
		}
	}
	if err := s.riderRepo.UpdateAvailability(ctx, riderID, available); err != nil {
		return fmt.Errorf("set rider availability: persist: %w", err)
	}
	return nil
}
