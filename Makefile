.PHONY: help run build test lint docker migrate-up migrate-down up down ps logs redpanda topics rpk

DATABASE_URL ?= postgres://dev:dev@localhost:5432/shortn?sslmode=disable
COMPOSE ?= docker compose -f deploy/compose/docker-compose.yml

help: ## list available targets (this menu)
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*## "}{printf "  %-13s %s\n", $$1, $$2}'

# ------------ app ------------
run: ## run the API locally
	go run ./cmd/api

build: ## compile everything
	go build ./...

test: ## run all tests
	go test ./...

lint: ## go vet + golangci-lint
	golangci-lint run

docker: ## build the api container image
	docker build --build-arg SERVICE=api -t shortn-api:dev .

# ------------ database ------------
migrate-up: ## apply all migrations
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down: ## roll back the last migration
	migrate -path migrations -database "$(DATABASE_URL)" down 1

# ------------ local stack (docker compose) ------------
up: ## start the whole stack in the background
	$(COMPOSE) up -d

down: ## stop the stack (data volumes are kept)
	$(COMPOSE) down

ps: ## show running services and their health
	$(COMPOSE) ps

logs: ## follow logs; pick one with SVC=, e.g. make logs SVC=redpanda
	$(COMPOSE) logs -f $(SVC)

# ------------ redpanda / kafka ------------
redpanda: ## start just Redpanda
	$(COMPOSE) up -d redpanda

topics: ## create the click-events topic (no-op if it already exists)
	$(COMPOSE) exec redpanda rpk topic create shortn.clicks -p 6 || true

rpk: ## run any rpk command, e.g. make rpk ARGS="cluster info"
	$(COMPOSE) exec redpanda rpk $(ARGS)
