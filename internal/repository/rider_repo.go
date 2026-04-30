package repository

/*
SQL Schema — run once during migrations:

CREATE TABLE riders (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             VARCHAR(255)     NOT NULL,
    is_available     BOOLEAN          NOT NULL DEFAULT TRUE,
    lat              DOUBLE PRECISION NOT NULL DEFAULT 0,
    lng              DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_assigned_at TIMESTAMPTZ
);

CREATE INDEX idx_riders_available ON riders(is_available);
*/

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"rider-assignment-service/internal/domain"
)

var ErrRiderNotFound = errors.New("rider not found")

// RiderRepository is the persistence contract for riders.
type RiderRepository interface {
	Create(ctx context.Context, rider *domain.Rider) error
	GetByID(ctx context.Context, id string) (*domain.Rider, error)
	UpdateAvailability(ctx context.Context, id string, available bool) error
	UpdateLocation(ctx context.Context, id string, lat, lng float64) error
	// MarkAssigned sets is_available=false and stamps last_assigned_at=NOW()
	// atomically. Called immediately after a rider is assigned an order so
	// future tie-breaking uses an accurate idle timestamp.
	MarkAssigned(ctx context.Context, id string) error
}

type postgresRiderRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRiderRepository(db *pgxpool.Pool) RiderRepository {
	return &postgresRiderRepository{db: db}
}

func (r *postgresRiderRepository) Create(ctx context.Context, rider *domain.Rider) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO riders (id, name, is_available, lat, lng)
		 VALUES ($1, $2, $3, $4, $5)`,
		rider.ID, rider.Name, rider.IsAvailable, rider.Lat, rider.Lng,
	)
	if err != nil {
		return fmt.Errorf("create rider: %w", err)
	}
	return nil
}

func (r *postgresRiderRepository) UpdateLocation(ctx context.Context, id string, lat, lng float64) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE riders SET lat = $2, lng = $3 WHERE id = $1`,
		id, lat, lng,
	)
	if err != nil {
		return fmt.Errorf("update rider location %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update rider location %s: %w", id, ErrRiderNotFound)
	}
	return nil
}

func (r *postgresRiderRepository) GetByID(ctx context.Context, id string) (*domain.Rider, error) {
	var (
		rider          domain.Rider
		lastAssignedAt pgtype.Timestamptz
	)
	err := r.db.QueryRow(ctx,
		`SELECT id, name, is_available, lat, lng, last_assigned_at
		 FROM riders WHERE id = $1`,
		id,
	).Scan(
		&rider.ID, &rider.Name, &rider.IsAvailable,
		&rider.Lat, &rider.Lng, &lastAssignedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get rider %s: %w", id, ErrRiderNotFound)
		}
		return nil, fmt.Errorf("get rider %s: %w", id, err)
	}
	if lastAssignedAt.Valid {
		rider.LastAssignedAt = lastAssignedAt.Time
	} else {
		// Zero time signals "never assigned"; tie-breaking treats this as longest idle.
		rider.LastAssignedAt = time.Time{}
	}
	return &rider, nil
}

func (r *postgresRiderRepository) UpdateAvailability(ctx context.Context, id string, available bool) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE riders SET is_available = $1 WHERE id = $2`,
		available, id,
	)
	if err != nil {
		return fmt.Errorf("update rider availability %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update rider availability %s: %w", id, ErrRiderNotFound)
	}
	return nil
}

func (r *postgresRiderRepository) MarkAssigned(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE riders SET is_available = false, last_assigned_at = NOW() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("mark rider assigned %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark rider assigned %s: %w", id, ErrRiderNotFound)
	}
	return nil
}
