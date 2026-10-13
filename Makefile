BALEIA_DIR ?= $(CURDIR)
PLUGIN_DIR ?= $(BALEIA_DIR)/.plugin/baleia-trino-catalog-store
PLUGIN_REPO ?= https://github.com/maltzsama/baleia-trino-catalog-store.git
PLUGIN_TAG  ?= v0.7.0
PLUGIN_JAVA_IMAGE ?= eclipse-temurin:25-jdk

# Fallback secret for local development. An exported or command-line
# JWT_SECRET wins, so credentials encrypted under a custom key stay readable.
JWT_SECRET ?= dev-secret-not-for-production

# Credentials are sealed with BALEIA_ENCRYPTION_KEY, never JWT_SECRET. Same
# override rule: an exported or command-line value wins.
BALEIA_ENCRYPTION_KEY ?= dev-encryption-key-not-for-production-0123

.PHONY: dev worker migrate migrate-credentials migrate-down migrate-status migrate-reset sqlc generate lint build build-web build-all seed-tpch plugin-clone plugin-build clean-plugin up down down-v logs restart-trino k8s-up k8s-plugin-bundle k8s-forward k8s-logs k8s-seed-tpch k8s-down

dev:
	JWT_SECRET=$(JWT_SECRET) BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) SMTP_HOST=localhost SMTP_PORT=1025 SMTP_FROM=noreply@baleia.io PORT=9000 go run ./cmd/api

worker:
	JWT_SECRET=$(JWT_SECRET) go run ./cmd/worker

# ── Trino plugin ──────────────────────────────────────────────────────────────
# Clones and builds the baleia-trino-catalog-store plugin using Docker.
# The output goes to $(PLUGIN_DIR)/target/baleia-catalog-store/ and is consumed
# by Trino in the compose file via a bind mount (docker-compose.yaml).
.PHONY: plugin-clone
plugin-clone:
	@if [ ! -d "$(PLUGIN_DIR)" ]; then \
		echo ">>> Cloning plugin $(PLUGIN_TAG) from $(PLUGIN_REPO)..."; \
		git clone --depth 1 --branch $(PLUGIN_TAG) $(PLUGIN_REPO) $(PLUGIN_DIR); \
	else \
		echo ">>> Plugin already cloned at $(PLUGIN_DIR)"; \
	fi

.PHONY: plugin-build
plugin-build: plugin-clone
	@echo ">>> Building plugin in $(PLUGIN_DIR)..."
	docker run --rm \
		-v "$(PLUGIN_DIR):/plugin" \
		-w /plugin \
		$(PLUGIN_JAVA_IMAGE) \
		sh -c 'apt-get update -qq && apt-get install -y -qq maven 2>/dev/null && mvn clean package -q -DskipTests'
	@echo ">>> Plugin built: $$(ls $(PLUGIN_DIR)/target/baleia-catalog-store/baleia-trino-catalog-store-*.jar 2>/dev/null)"

.PHONY: clean-plugin
clean-plugin:
	rm -rf $(PLUGIN_DIR)

# ── Compose ───────────────────────────────────────────────────────────────────

# up: builds the plugin and brings up the full stack.
up: plugin-build
	docker compose up -d

# down: tears down the stack without destroying volumes (data persists).
down:
	docker compose down

# down-v: tears down the stack and removes named volumes + stopped containers +
# orphaned volumes. WARNING: destroys postgres, temporal, etc. data.
down-v:
	docker compose rm -fsv
	docker compose down -v
	docker container prune -f
	docker volume prune -f

logs:
	docker compose logs -f api worker

restart-trino:
	docker compose restart trino

# ── Migrations ────────────────────────────────────────────────────────────────

migrate:
	go run ./cmd/migrate -cmd=up

migrate-down:
	go run ./cmd/migrate -cmd=down

migrate-status:
	go run ./cmd/migrate -cmd=status

migrate-reset:
	go run ./cmd/migrate -cmd=reset

# One-shot: re-seal legacy connection credentials (encrypted under JWT_SECRET)
# as v1 envelopes under BALEIA_ENCRYPTION_KEY. Idempotent. Needs DATABASE_URL.
migrate-credentials:
	JWT_SECRET=$(JWT_SECRET) BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) go run ./cmd/migrate -cmd=credentials

# ── Seed ──────────────────────────────────────────────────────────────────────

# Inserts the tpch_test test row into trino_catalog_registry, used by the
# T1-T9 acceptance tests of the baleia-trino-catalog-store plugin. Idempotent.
seed-tpch:
	docker compose exec -T postgres psql -U baleia -d baleia < db/seed/tpch_test.sql

# ── Code Generation ───────────────────────────────────────────────────────────

sqlc:
	sqlc generate

generate:
	oapi-codegen --config=config/oapi-models.yaml api/swagger.yaml
	oapi-codegen --config=config/oapi-server.yaml api/swagger.yaml

# ── Build ─────────────────────────────────────────────────────────────────────

lint:
	golangci-lint run ./...

build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

build-web:
	cd web && npm run build

build-all: build-web build

# ── Kubernetes (minikube) ─────────────────────────────────────────────────────

K8S_PROFILE ?= baleia
K8S_NS      ?= baleia-dev
K8S_PLUGIN_IMAGE      ?= baleia-plugins
K8S_PLUGIN_TAG        ?= dev
K8S_PLUGIN_VERSION    ?= 0.7.0

# Bundle the RELEASED catalog-store plugin (deploy/dev/plugins.Dockerfile
# downloads the GitHub-release tarball and verifies its sha256) into a sidecar
# image for the Trino initContainer. Builds on the host docker and loads into
# minikube (the node's overlay is small and GitHub may be blocked there).
k8s-plugin-bundle:
	docker build --build-arg K8S_PLUGIN_VERSION=$(K8S_PLUGIN_VERSION) \
		-t $(K8S_PLUGIN_IMAGE):$(K8S_PLUGIN_TAG) -f deploy/dev/plugins.Dockerfile .
	minikube -p $(K8S_PROFILE) image load $(K8S_PLUGIN_IMAGE):$(K8S_PLUGIN_TAG)

# Start minikube, deploy the dependency stack, build baleia:dev into the node,
# then install the chart.
k8s-up:
	minikube start -p $(K8S_PROFILE) --cpus=6 --memory=12g
	kubectl apply -k deploy/dev
	minikube -p $(K8S_PROFILE) image build -t baleia:dev .
	$(MAKE) k8s-plugin-bundle
	helm upgrade --install baleia deploy/helm/baleia -n $(K8S_NS) \
		-f deploy/dev/values-dev.yaml --wait --timeout 10m
	@echo ">>> api:   kubectl -n $(K8S_NS) port-forward svc/baleia-api 8080:8080"
	@echo ">>> usage: make k8s-forward && scripts/smoke.sh"

# Port-forward the UIs for local debugging.
k8s-forward:
	kubectl -n $(K8S_NS) port-forward svc/baleia-api 8080:8080 &
	kubectl -n $(K8S_NS) port-forward svc/polaris-console 4000:80 &
	kubectl -n $(K8S_NS) port-forward svc/mailpit 8025:8025 &

k8s-logs:
	kubectl -n $(K8S_NS) logs -f -l app.kubernetes.io/name=baleia --all-containers

k8s-seed-tpch:
	kubectl -n $(K8S_NS) exec -i statefulset/postgres -- psql -U baleia -d baleia < db/seed/tpch_test.sql

k8s-down:
	helm -n $(K8S_NS) uninstall baleia || true
	kubectl delete -k deploy/dev || true
	if [ -n "$${K8S_PURGE:-}" ]; then \
		minikube -p $(K8S_PROFILE) delete; \
	fi
