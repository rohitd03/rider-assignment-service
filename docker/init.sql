-- riders must be created before orders because orders.rider_id references it.

CREATE TABLE IF NOT EXISTS riders (
    id               UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    name             VARCHAR(255)     NOT NULL,
    is_available     BOOLEAN          NOT NULL DEFAULT TRUE,
    lat              DOUBLE PRECISION NOT NULL DEFAULT 0,
    lng              DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_assigned_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_riders_available ON riders(is_available);

CREATE TABLE IF NOT EXISTS orders (
    id         UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    rider_id   UUID             REFERENCES riders(id),
    status     VARCHAR(20)      NOT NULL DEFAULT 'CREATED',
    pickup_lat DOUBLE PRECISION NOT NULL,
    pickup_lng DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
