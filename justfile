pg_url := env("MEND_PG_URL", "postgres://mend:mend@localhost:55432/postgres?sslmode=disable")

# Start the scratch Postgres.
up:
    docker compose up -d --wait

# Stop the scratch Postgres (its data is discarded).
down:
    docker compose down

# Unit tests; no database needed.
test:
    go test ./...

# All tests, including those that need Postgres.
test-integration: up
    MEND_PG_URL='{{pg_url}}' go test -count=1 ./...

# Run the pricing-bot incident and compare every recovery strategy.
harness *args: up
    MEND_PG_URL='{{pg_url}}' go run ./cmd/mend-harness -json results/pricing-bot.json {{args}}
