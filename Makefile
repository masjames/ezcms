COMPOSE ?= docker compose
DB_URL ?= postgres://ezcms:ezcms@localhost:5433/ezcms?sslmode=disable
GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: up down logs migrate api web install test fmt

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down -v

logs:
	$(COMPOSE) logs -f

migrate:
	for file in db/migrations/*.sql; do \
		echo "Applying $$file"; \
		$(COMPOSE) exec -T postgres psql postgresql://ezcms:ezcms@localhost:5432/ezcms -v ON_ERROR_STOP=1 < $$file; \
	done

api:
	set -a && . ./.env && set +a && cd api && GOCACHE=$(GOCACHE) go run ./cmd/ezcms-api

web:
	cd web && npm run dev

install:
	cd api && GOCACHE=$(GOCACHE) go mod tidy
	cd web && npm install

fmt:
	cd api && gofmt -w $$(find . -name '*.go')

test:
	set -a && . ./.env && set +a && cd api && GOCACHE=$(GOCACHE) go test ./...
