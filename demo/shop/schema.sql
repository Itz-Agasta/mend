CREATE TABLE products (
    id          bigint PRIMARY KEY,
    sku         text   NOT NULL UNIQUE,
    category    text   NOT NULL,
    price_cents bigint NOT NULL CHECK (price_cents >= 0),
    stock       bigint NOT NULL CHECK (stock >= 0)
);

CREATE TABLE orders (
    id               bigserial   PRIMARY KEY,
    customer         text        NOT NULL,
    product_id       bigint      NOT NULL REFERENCES products (id),
    qty              bigint      NOT NULL CHECK (qty > 0),
    unit_price_cents bigint      NOT NULL,
    total_cents      bigint      NOT NULL,
    created_at       timestamptz NOT NULL
);
