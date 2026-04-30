package repository

/*
SQL Schema — run once during migrations:

CREATE TABLE orders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rider_id    UUID REFERENCES riders(id),
    status      VARCHAR(20)        NOT NULL DEFAULT 'CREATED',
    pickup_lat  DOUBLE PRECISION   NOT NULL,
    pickup_lng  DOUBLE PRECISION   NOT NULL,
    created_at  TIMESTAMPTZ        NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_orders_status ON orders(status);
*/

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rider-assignment-service/internal/domain"
)

var ErrOrderNotFound = errors.New("order not found")

// OrderRepository is the persistence contract for orders.
type OrderRepository interface {
	Create(ctx context.Context, order *domain.Order) error
	GetByID(ctx context.Context, id string) (*domain.Order, error)
	UpdateStatus(ctx context.Context, id string, status domain.OrderStatus) error
	// AssignRider atomically sets status=ASSIGNED and rider_id in one statement.
	// It only succeeds when the current DB status is CREATED, guarding against
	// concurrent double-assignment at the storage level.
	AssignRider(ctx context.Context, orderID, riderID string) error
}

type postgresOrderRepository struct {
	db *pgxpool.Pool
}

func NewPostgresOrderRepository(db *pgxpool.Pool) OrderRepository {
	return &postgresOrderRepository{db: db}
}

func (r *postgresOrderRepository) Create(ctx context.Context, order *domain.Order) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO orders (id, rider_id, status, pickup_lat, pickup_lng, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		order.ID, order.RiderID, string(order.Status),
		order.PickupLat, order.PickupLng, order.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create order: %w", err)
	}
	return nil
}

func (r *postgresOrderRepository) GetByID(ctx context.Context, id string) (*domain.Order, error) {
	var o domain.Order
	err := r.db.QueryRow(ctx,
		`SELECT id, rider_id, status, pickup_lat, pickup_lng, created_at
		 FROM orders WHERE id = $1`,
		id,
	).Scan(&o.ID, &o.RiderID, &o.Status, &o.PickupLat, &o.PickupLng, &o.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get order %s: %w", id, ErrOrderNotFound)
		}
		return nil, fmt.Errorf("get order %s: %w", id, err)
	}
	return &o, nil
}

func (r *postgresOrderRepository) UpdateStatus(ctx context.Context, id string, status domain.OrderStatus) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE orders SET status = $1 WHERE id = $2`,
		string(status), id,
	)
	if err != nil {
		return fmt.Errorf("update order status %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update order status %s: %w", id, ErrOrderNotFound)
	}
	return nil
}

// AssignRider atomically sets status=ASSIGNED and rider_id only when the
// current row status is CREATED. RowsAffected==0 means a concurrent request
// already assigned this order.
func (r *postgresOrderRepository) AssignRider(ctx context.Context, orderID, riderID string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE orders SET status = 'ASSIGNED', rider_id = $2
		 WHERE id = $1 AND status = 'CREATED'`,
		orderID, riderID,
	)
	if err != nil {
		return fmt.Errorf("assign rider to order %s: %w", orderID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("assign rider to order %s: %w", orderID, ErrOrderNotFound)
	}
	return nil
}
