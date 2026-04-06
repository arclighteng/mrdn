.PHONY: help db db-stop db-reset migrate seed setup serve ingest test lint build clean

DATABASE_URL ?= postgresql://mrdn:mrdn@localhost:5432/mrdn?sslmode=disable
export DATABASE_URL

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

# --- Database ---

db: ## Start Postgres (docker compose)
	docker compose up -d postgres
	@echo "Waiting for Postgres..."
	@docker compose exec postgres sh -c 'until pg_isready -U mrdn; do sleep 0.5; done' > /dev/null 2>&1
	@echo "Postgres ready on :5432"

db-stop: ## Stop Postgres
	docker compose down

db-reset: ## Destroy and recreate Postgres volume
	docker compose down -v
	$(MAKE) db

# --- App lifecycle ---

migrate: ## Run database migrations
	go run ./cmd/mrdn migrate

seed: ## Seed companies
	go run ./cmd/mrdn seed

setup: db migrate seed ## Full dev setup: db + migrate + seed

serve: ## Start API server (port 8080)
	go run ./cmd/mrdn serve

ingest: ## Start ingestion workers (requires API keys in env)
	go run ./cmd/mrdn ingest

# --- Dev ---

test: ## Run all tests
	go test ./... -race -count=1

lint: ## Run go vet
	go vet ./...

build: ## Build binary
	go build -o mrdn ./cmd/mrdn

clean: ## Remove binary and coverage
	rm -f mrdn coverage.out
