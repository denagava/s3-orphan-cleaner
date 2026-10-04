COMPOSE := docker compose -f docker/docker-compose.yml

.PHONY: build run test test-integration docker-up docker-seed docker-run docker-down

build:
	go build -o bin/s3-orphan-cleaner ./cmd/cleaner

run: build
	./bin/s3-orphan-cleaner

test:
	go test ./internal/... -v

test-integration:
	go test -tags integration ./tests/integration/... -v -timeout=5m

docker-up:
	$(COMPOSE) up -d postgres minio

docker-seed:
	go run ./docker/seeds/seed.go

docker-run:
	$(COMPOSE) run --rm app

docker-down:
	$(COMPOSE) down -v
