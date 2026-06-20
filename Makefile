.PHONY: help run run-analytics build test test-integration lint docker docker-analytics images migrate-up migrate-down up down ps logs redpanda topics rpk chaos load kind-up kind-down kind-load k8s-deploy k8s-migrate k8s-up k8s-down k8s-status k8s-logs

DATABASE_URL ?= postgres://dev:dev@localhost:5432/shortn?sslmode=disable
COMPOSE ?= docker compose -f deploy/compose/docker-compose.yml

# kind cluster name + the local image tags loaded into it (override on the CLI if needed)
KIND_CLUSTER ?= shortn
API_IMAGE ?= shortn-api:dev
ANALYTICS_IMAGE ?= shortn-analytics:dev

help: ## list available targets (this menu)
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*## "}{printf "  %-16s %s\n", $$1, $$2}'

# ------------ app ------------
run: ## run the API locally
	go run ./cmd/api

run-analytics: ## run the analytics consumer locally
	go run ./cmd/analytics

build: ## compile everything
	go build ./...

test: ## run all tests
	go test ./...

test-integration: ## run integration tests (real Postgres via testcontainers; needs Docker)
	go test -tags=integration ./...

lint: ## go vet + golangci-lint
	golangci-lint run

docker: ## build the api container image
	docker build --build-arg SERVICE=api -t $(API_IMAGE) .

docker-analytics: ## build the analytics image (same Dockerfile, SERVICE=analytics)
	docker build --build-arg SERVICE=analytics -t $(ANALYTICS_IMAGE) .

images: docker docker-analytics ## build both service images at once

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

prune: ## remove all dangling images that are not being used by a running container
	docker image prune

##  This will remove: all stopped containers, all networks not used by at least one container, 
##  all dangling images, unused build cache
system-prune: 
	docker system prune

# ------------ redpanda / kafka ------------
redpanda: ## start just Redpanda
	$(COMPOSE) up -d redpanda

topics: ## create the click-events topic (no-op if it already exists)
	$(COMPOSE) exec redpanda rpk topic create shortn.clicks -p 6 || true

rpk: ## run any rpk command, e.g. make rpk ARGS="cluster info"
	$(COMPOSE) exec redpanda rpk $(ARGS)

# ------------ resilience ------------
chaos: ## take each dependency down in turn and assert documented behavior (docs/runbook.md)
	./scripts/chaos.sh

# ------------ observability ------------
load: ## drive preview traffic so the Phase 6 dashboards move (k6 via docker; needs the stack up)
	docker run --rm -i --network shortn_default -e BASE_URL=http://nginx grafana/k6 run - < load/preview.js

# ------------ kubernetes (kind) ------------
kind-up: ## create the local kind cluster (no-op if it already exists)
	kind get clusters | grep -qx $(KIND_CLUSTER) || kind create cluster --name $(KIND_CLUSTER)

kind-down: ## delete the kind cluster (wipes the whole cluster)
	kind delete cluster --name $(KIND_CLUSTER)

kind-load: ## copy the built api+analytics images into the cluster's image store
	kind load docker-image $(API_IMAGE) $(ANALYTICS_IMAGE) --name $(KIND_CLUSTER)

k8s-deploy: ## apply the service manifests (deps first, then api + analytics)
	kubectl apply -f deploy/k8s/postgres/ -f deploy/k8s/redis/ -f deploy/k8s/redpanda/ -f deploy/k8s/api/ -f deploy/k8s/analytics/

k8s-migrate: ## (re)build the migrations ConfigMap from migrations/ and run the migrate Job
	kubectl create configmap shortn-migrations --from-file=migrations/ --dry-run=client -o yaml | kubectl apply -f -
	kubectl delete job shortn-migrate --ignore-not-found
	kubectl apply -f deploy/k8s/migrate-job.yaml

k8s-up: kind-up images kind-load k8s-deploy k8s-migrate ## one button: cluster + images + manifests + migrations
	@echo "shortn is coming up — watch the pods settle with: make k8s-status"

k8s-down: ## delete shortn workloads (keeps the cluster and its data PVCs)
	kubectl delete -f deploy/k8s/postgres/ -f deploy/k8s/redis/ -f deploy/k8s/redpanda/ -f deploy/k8s/api/ -f deploy/k8s/analytics/ --ignore-not-found

k8s-status: ## pods, services, statefulsets, jobs at a glance
	kubectl get pods,svc,statefulset,job

k8s-logs: ## tail one service, e.g. make k8s-logs APP=shortn-api (or shortn-analytics)
	kubectl logs -f deploy/$(APP)
