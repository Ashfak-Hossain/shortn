.PHONY: help run run-analytics build test test-integration lint docker docker-analytics images migrate-up migrate-down up up-build down ps logs nginx-reload redpanda topics rpk chaos load load-redirect load-create kind-up kind-down kind-stop kind-start kind-load k8s-ingress-controller metrics-server argocd-install sealed-secrets seal-key-backup seal-key-restore argocd-app argocd-password argocd-ui helm-install helm-uninstall k8s-migrate k8s-up k8s-status k8s-logs k8s-load k8s-load-stop tf-init tf-plan tf-apply tf-destroy

DATABASE_URL ?= postgres://dev:dev@localhost:5432/shortn?sslmode=disable
COMPOSE ?= docker compose -f deploy/compose/docker-compose.yml

# kind cluster name + the local image tags loaded into it (override on the CLI if needed)
KIND_CLUSTER ?= shortn
KIND_CONFIG ?= deploy/k8s/kind-config.yaml
API_IMAGE ?= shortn-api:dev
ANALYTICS_IMAGE ?= shortn-analytics:dev
# pin to a tag (e.g. controller-v1.12.1) for reproducibility; main always resolves
INGRESS_NGINX_REF ?= main
# pin to e.g. v2.13.0 for reproducibility; stable always resolves
ARGOCD_REF ?= stable
HELM_RELEASE ?= shortn
CHART ?= deploy/k8s/shortn
TF_DIR ?= deploy/terraform
SEAL_KEY_BACKUP ?= .secrets/sealed-secrets-key.yaml

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

up-build: ## rebuild images from source, then start the stack (use after Go edits)
	$(COMPOSE) up -d --build

down: ## stop the stack (data volumes are kept)
	$(COMPOSE) down

ps: ## show running services and their health
	$(COMPOSE) ps

logs: ## follow logs; pick one with SVC=, e.g. make logs SVC=redpanda
	$(COMPOSE) logs -f $(SVC)

nginx-reload: ## apply nginx.conf edits (recreates the container — robust vs the macOS bind-mount atomic-save staleness)
	$(COMPOSE) up -d --force-recreate nginx

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

# ------------ load testing (k6) ------------
# Defaults hit the compose stack's nginx on :80 (where the Grafana dashboards live).
# For the kind ingress: make load-redirect LOAD_BASE_URL=http://localhost LOAD_HOST=shortn.localhost
LOAD_BASE_URL ?= http://localhost
LOAD_HOST ?=
LOAD_RATE ?= 1000
LOAD_DURATION ?= 1m
LOAD_POOL ?= 200

load-redirect: ## k6 open-model redirect-heavy run (override LOAD_RATE / LOAD_DURATION / LOAD_BASE_URL / LOAD_HOST)
	k6 run -e BASE_URL=$(LOAD_BASE_URL) -e HOST_HEADER=$(LOAD_HOST) -e RATE=$(LOAD_RATE) -e DURATION=$(LOAD_DURATION) -e POOL=$(LOAD_POOL) load/redirect.js

load-create: ## k6 create-heavy run (write path; same LOAD_* overrides)
	k6 run -e BASE_URL=$(LOAD_BASE_URL) -e HOST_HEADER=$(LOAD_HOST) -e RATE=$(LOAD_RATE) -e DURATION=$(LOAD_DURATION) load/create.js

# ------------ kubernetes (kind) ------------
kind-up: ## create the local kind cluster (no-op if it already exists)
	kind get clusters | grep -qx $(KIND_CLUSTER) || kind create cluster --name $(KIND_CLUSTER) --config $(KIND_CONFIG)

kind-down: ## delete the kind cluster (wipes the whole cluster)
	kind delete cluster --name $(KIND_CLUSTER)

kind-stop: ## pause the cluster: stop its node container (frees :80/:443 for compose; cluster + data survive)
	docker stop $(KIND_CLUSTER)-control-plane

kind-start: ## resume a paused cluster: start its node container back up
	docker start $(KIND_CLUSTER)-control-plane

kind-load: ## copy the built api+analytics images into the cluster's image store
	kind load docker-image $(API_IMAGE) $(ANALYTICS_IMAGE) --name $(KIND_CLUSTER)

k8s-ingress-controller: ## install the nginx ingress controller (kind provider) + wait until ready
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/$(INGRESS_NGINX_REF)/deploy/static/provider/kind/deploy.yaml
	kubectl rollout status deployment/ingress-nginx-controller -n ingress-nginx --timeout=120s

metrics-server: ## install metrics-server (the HPA's CPU source); patch --kubelet-insecure-tls for kind
	kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
	kubectl patch deployment metrics-server -n kube-system --type=json \
		-p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
	kubectl rollout status deployment/metrics-server -n kube-system --timeout=120s

argocd-install: ## install ArgoCD into the cluster + wait for its server
	kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -n argocd --server-side --force-conflicts -f https://raw.githubusercontent.com/argoproj/argo-cd/$(ARGOCD_REF)/manifests/install.yaml
	kubectl rollout status deployment/argocd-server -n argocd --timeout=300s

sealed-secrets: ## install the Sealed Secrets controller (decrypts SealedSecrets in-cluster)
	kubectl apply -f https://github.com/bitnami-labs/sealed-secrets/releases/latest/download/controller.yaml
	kubectl rollout status deployment/sealed-secrets-controller -n kube-system --timeout=120s

seal-key-backup: ## save the controller's sealing key to .secrets/ (gitignored) — keep it safe; it decrypts your SealedSecrets
	@mkdir -p $(dir $(SEAL_KEY_BACKUP))
	kubectl get secret -n kube-system -l sealedsecrets.bitnami.com/sealed-secrets-key -o yaml > $(SEAL_KEY_BACKUP)
	@echo "backed up sealing key -> $(SEAL_KEY_BACKUP)"

seal-key-restore: ## restore the backed-up sealing key onto a fresh cluster, then restart the controller to load it
	kubectl apply -f $(SEAL_KEY_BACKUP) || echo "  apply skipped — key already present; restarting controller to load it"
	kubectl rollout restart deployment sealed-secrets-controller -n kube-system
	kubectl rollout status deployment sealed-secrets-controller -n kube-system --timeout=120s

argocd-app: ## register the shortn Application; ArgoCD then syncs the chart from git
	kubectl apply -f deploy/k8s/argocd/shortn-application.yaml

argocd-password: ## print the initial ArgoCD admin password
	@kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d; echo

argocd-ui: ## port-forward the ArgoCD UI to https://localhost:8081 (user: admin)
	kubectl port-forward -n argocd svc/argocd-server 8081:443

helm-install: ## install/upgrade the shortn chart with dev values (idempotent)
	helm upgrade --install $(HELM_RELEASE) $(CHART) -f $(CHART)/values-dev.yaml

helm-uninstall: ## remove the shortn release (PVCs survive)
	helm uninstall $(HELM_RELEASE)

k8s-migrate: ## (re)build the migrations ConfigMap from migrations/ and run the migrate Job
	kubectl rollout status statefulset/shortn-postgres --timeout=120s
	kubectl create configmap shortn-migrations --from-file=migrations/ --dry-run=client -o yaml | kubectl apply -f -
	kubectl delete job shortn-migrate --ignore-not-found
	kubectl apply -f deploy/k8s/migrate-job.yaml

k8s-up: kind-up k8s-ingress-controller metrics-server sealed-secrets images kind-load helm-install k8s-migrate ## one button: cluster + ingress + metrics + sealed-secrets + images + chart + migrations
	@echo "shortn is coming up — watch the pods settle with: make k8s-status"

k8s-status: ## pods, services, statefulsets, jobs at a glance
	kubectl get pods,svc,statefulset,job

k8s-logs: ## tail one service, e.g. make k8s-logs APP=shortn-api (or shortn-analytics)
	kubectl logs -f deploy/$(APP)

k8s-load: ## drive CPU load from an in-cluster pod to trigger the HPA (stop with k8s-load-stop)
	kubectl run loadgen --image=busybox:1.28 --restart=Never -- /bin/sh -c 'for i in $$(seq 1 40); do (while true; do wget -q -O- http://shortn-api:8080/healthz; done) & done; wait'

k8s-load-stop: ## stop and remove the load generator pod
	kubectl delete pod loadgen --ignore-not-found

# ------------ terraform (iac) ------------
tf-init: ## terraform: download providers (run once)
	terraform -chdir=$(TF_DIR) init

tf-plan: ## terraform: preview what would change vs state
	terraform -chdir=$(TF_DIR) plan

tf-apply: ## terraform: create/update the cluster + ArgoCD from code
	terraform -chdir=$(TF_DIR) apply

tf-destroy: ## terraform: tear down everything Terraform manages
	terraform -chdir=$(TF_DIR) destroy
