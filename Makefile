.PHONY: help run run-analytics build test test-integration lint docker docker-analytics images migrate-up migrate-down db-backup db-restore db-verify up up-build down ps logs nginx-reload redpanda topics rpk chaos load load-redirect load-create kind-up kind-down kind-stop kind-start kind-load k8s-ingress-controller metrics-server argocd-install sealed-secrets seal-key-backup seal-key-restore argocd-app argocd-password argocd-ui helm-install helm-uninstall k8s-migrate k8s-up k8s-status k8s-logs k8s-load k8s-load-stop tf-init tf-plan tf-apply tf-destroy azure-kubeconfig azure-tunnel azure-nodes azure-secret azure-deploy azure-status azure-migrate azure-logs azure-psql azure-admin-key azure-metrics azure-image

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

DB_BACKUP_DIR ?= backups

db-backup: ## dump the compose Postgres (compressed custom format) into $(DB_BACKUP_DIR)/
	@mkdir -p $(DB_BACKUP_DIR)
	$(COMPOSE) exec -T postgres pg_dump -U dev -Fc shortn > $(DB_BACKUP_DIR)/shortn-$$(date +%Y%m%d-%H%M%S).dump
	@echo "backup written under $(DB_BACKUP_DIR)/"

db-restore: ## restore a dump into the compose Postgres: make db-restore FILE=backups/<file>.dump (DROPS + recreates objects)
	@test -n "$(FILE)" || { echo "usage: make db-restore FILE=$(DB_BACKUP_DIR)/<file>.dump"; exit 1; }
	$(COMPOSE) exec -T postgres pg_restore -U dev -d shortn --clean --if-exists < $(FILE)
	@echo "restored from $(FILE)"

db-verify: ## restore drill: load the latest dump into a scratch DB and compare row counts (an untested backup is not a backup)
	@set -e; \
	dump=$$(ls -t $(DB_BACKUP_DIR)/*.dump 2>/dev/null | head -1); \
	test -n "$$dump" || { echo "no dump in $(DB_BACKUP_DIR)/ — run 'make db-backup' first"; exit 1; }; \
	echo "restoring $$dump into scratch DB shortn_verify ..."; \
	$(COMPOSE) exec -T postgres dropdb -U dev --if-exists shortn_verify; \
	$(COMPOSE) exec -T postgres createdb -U dev shortn_verify; \
	$(COMPOSE) exec -T postgres pg_restore -U dev -d shortn_verify <"$$dump"; \
	echo "live   links = $$($(COMPOSE) exec -T postgres psql -U dev -d shortn        -tAc 'select count(*) from links')"; \
	echo "restored links = $$($(COMPOSE) exec -T postgres psql -U dev -d shortn_verify -tAc 'select count(*) from links')"; \
	$(COMPOSE) exec -T postgres dropdb -U dev shortn_verify; \
	echo "verified — scratch DB dropped"

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

k8s-up: kind-up k8s-ingress-controller metrics-server sealed-secrets seal-key-restore images kind-load helm-install k8s-migrate ## one button (from scratch): cluster + ingress + metrics + sealed-secrets + KEY RESTORE + images + chart + migrations
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

# ------------ azure (live k3s deploy) ------------
AZURE_IP ?= 20.205.250.1
AZURE_SSH_KEY ?= ~/.ssh/shortn_azure
AZURE_KUBECONFIG ?= $(HOME)/.kube/shortn-azure.yaml
# every azure-* command below targets the LIVE cluster through the tunnel:
AZ_KUBECTL = KUBECONFIG=$(AZURE_KUBECONFIG) kubectl
AZ_HELM = KUBECONFIG=$(AZURE_KUBECONFIG) helm

azure-kubeconfig: ## copy the cluster's kubeconfig from the VM to the laptop (run once)
	scp -i $(AZURE_SSH_KEY) azureuser@$(AZURE_IP):.kube/config $(AZURE_KUBECONFIG)
	@echo "saved -> $(AZURE_KUBECONFIG)"

azure-tunnel: ## open the SSH tunnel to the k8s API — RUN IN ITS OWN TERMINAL, leave it open
	ssh -i $(AZURE_SSH_KEY) -o ServerAliveInterval=60 -o ServerAliveCountMax=3 \
	  -L 6443:127.0.0.1:6443 -N azureuser@$(AZURE_IP)

azure-nodes: ## check the laptop can reach the live cluster (tunnel must be up)
	$(AZ_KUBECTL) get nodes

azure-secret: ## create/update the API Secret (DB/Redis/ADMIN_KEY) on the live cluster; pass ADMIN_KEY=... (tunnel must be up)
	@test -n "$(ADMIN_KEY)" || { echo "ERROR: set ADMIN_KEY=... (generate one with: openssl rand -hex 32)"; exit 1; }
	$(AZ_KUBECTL) create secret generic shortn-api-secret \
	  --from-literal=DATABASE_URL='postgres://dev:dev@shortn-postgres:5432/shortn?sslmode=disable' \
	  --from-literal=REDIS_URL='redis://shortn-redis:6379/0' \
	  --from-literal=ADMIN_KEY='$(ADMIN_KEY)' \
	  --dry-run=client -o yaml | $(AZ_KUBECTL) apply -f -

azure-deploy: ## install/upgrade the lean chart on the live cluster (tunnel must be up)
	$(AZ_HELM) upgrade --install $(HELM_RELEASE) $(CHART) \
	  -f $(CHART)/values-azure.yaml --set ingress.host=$(AZURE_IP).nip.io

azure-status: ## pods / services / ingress on the live cluster (tunnel must be up)
	$(AZ_KUBECTL) get pods,svc,ingress

azure-migrate: ## run DB migrations on the live Postgres (temporary port-forward; tunnel + migrate CLI needed)
	$(AZ_KUBECTL) port-forward svc/shortn-postgres 5433:5432 & \
	  pf=$$!; sleep 3; \
	  migrate -path migrations -database 'postgres://dev:dev@127.0.0.1:5433/shortn?sslmode=disable' up; \
	  kill $$pf

azure-logs: ## tail the live API logs (tunnel must be up)
	$(AZ_KUBECTL) logs -l app=shortn-api --tail=100 -f

azure-psql: ## open a psql shell on the live Postgres — \dt to list tables (tunnel must be up)
	$(AZ_KUBECTL) exec -it shortn-postgres-0 -- psql -U dev -d shortn

azure-admin-key: ## print the ADMIN_KEY currently stored in the live Secret (tunnel must be up)
	@$(AZ_KUBECTL) get secret shortn-api-secret -o jsonpath='{.data.ADMIN_KEY}' | base64 -d; echo

azure-metrics: ## fetch /metrics from the API's internal ops port (9090, not public; tunnel must be up)
	$(AZ_KUBECTL) port-forward deploy/shortn-api 9090:9090 & \
	  pf=$$!; sleep 3; \
	  curl -s localhost:9090/metrics | grep -E '^http_server' | head -30; \
	  kill $$pf

azure-image: ## rebuild + push a fresh amd64 API image tagged with the current commit
	docker buildx build --platform linux/amd64 --build-arg SERVICE=api \
	  -t ghcr.io/ashfak-hossain/shortn-api:$$(git rev-parse --short HEAD) --push .
	@echo "pushed shortn-api:$$(git rev-parse --short HEAD) — set this tag in $(CHART)/values-azure.yaml, then run: make azure-deploy"
