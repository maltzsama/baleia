BALEIA_DIR ?= $(CURDIR)

# The baleia-trino-catalog-store release Baleia runs and tests its contract
# against (#88, #39): the single pin for compose, the dev cluster and CI.
BALEIA_PLUGIN_VERSION ?= 0.9.1
TRINO_VERSION         ?= 483
BALEIA_PLUGIN_TARBALL  = baleia-catalog-store-$(BALEIA_PLUGIN_VERSION)+trino$(TRINO_VERSION).tar.gz
BALEIA_PLUGIN_URL     ?= https://github.com/maltzsama/baleia-trino-catalog-store/releases/download/v$(BALEIA_PLUGIN_VERSION)/$(BALEIA_PLUGIN_TARBALL)

# Fallback secret for local development. An exported or command-line
# JWT_SECRET wins, so credentials encrypted under a custom key stay readable.
JWT_SECRET ?= dev-secret-not-for-production

# Credentials are sealed with BALEIA_ENCRYPTION_KEY, never JWT_SECRET. Same
# override rule: an exported or command-line value wins.
BALEIA_ENCRYPTION_KEY ?= dev-encryption-key-not-for-production-0123

.PHONY: dev worker migrate migrate-credentials migrate-down migrate-status migrate-reset sqlc generate lint build build-web build-all seed-tpch contract-fetch up down down-v logs restart-trino k8s-up k8s-plugin-bundle k8s-forward k8s-logs k8s-seed-tpch k8s-down k8s-source k8s-source-down dialect-lists

dev:
	JWT_SECRET=$(JWT_SECRET) BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) BALEIA_CONNECTIONS_ALLOW_LOOPBACK=true SMTP_HOST=localhost SMTP_PORT=1025 SMTP_FROM=noreply@baleia.io PORT=9000 go run ./cmd/api

worker:
	JWT_SECRET=$(JWT_SECRET) BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) BALEIA_CONNECTIONS_ALLOW_LOOPBACK=true go run ./cmd/worker

# ── Trino plugin ──────────────────────────────────────────────────────────────
# The plugin is never built here: compose and the dev cluster install the
# released tarball (sha256-verified) into the official Trino image.

# contract-fetch extracts contract/ of the pinned release into
# internal/contract/testdata for the contract tests (go test -tags contract).
.PHONY: contract-fetch
contract-fetch:
	@rm -rf internal/contract/testdata && mkdir -p internal/contract/testdata
	curl -fsSL -o internal/contract/testdata/p.tgz "$(BALEIA_PLUGIN_URL)"
	curl -fsSL -o internal/contract/testdata/p.sha256 "$(BALEIA_PLUGIN_URL).sha256"
	cd internal/contract/testdata && echo "$$(cut -d' ' -f1 p.sha256)  p.tgz" | sha256sum -c - \
		&& tar -xzf p.tgz contract VERSION \
		&& test "$$(cat VERSION)" = "$(BALEIA_PLUGIN_VERSION)+trino$(TRINO_VERSION)" \
		&& rm p.tgz p.sha256
	@echo ">>> contract of plugin $$(cat internal/contract/testdata/VERSION) in internal/contract/testdata"

# ── Compose ───────────────────────────────────────────────────────────────────

# up: brings up the full stack; trino-plugin-init installs the plugin release.
up:
	BALEIA_PLUGIN_VERSION=$(BALEIA_PLUGIN_VERSION) TRINO_VERSION=$(TRINO_VERSION) docker compose up -d

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
K8S_PLUGIN_VERSION    ?= $(BALEIA_PLUGIN_VERSION)
# The bundle image is tagged with the plugin release it carries (the plugin repo
# owns that version), so it matches trino.plugin.image in deploy/dev/values-dev.yaml.
K8S_PLUGIN_TAG        ?= $(K8S_PLUGIN_VERSION)

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
		-f deploy/dev/values-dev.yaml \
		--set trino.plugin.image=$(K8S_PLUGIN_IMAGE):$(K8S_PLUGIN_TAG) \
		--set trino.plugin.version=$(K8S_PLUGIN_VERSION) \
		--wait --timeout 10m
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

# Start one source database (deploy/dev/sources/<NAME>.yaml) for provider and
# dialect tests; k8s-source-down removes it. scripts/gen-dialect-lists.sh
# starts and removes them by itself.
k8s-source:
	kubectl apply -f deploy/dev/sources/$(NAME).yaml
	kubectl -n $(K8S_NS) rollout status deployment/src-$(NAME) --timeout=15m

k8s-source-down:
	kubectl delete -f deploy/dev/sources/$(NAME).yaml

# Regenerate every dialect's keyword and function lists from real servers on
# the dev cluster, running each dialect's execution test (one source at a time).
dialect-lists:
	for d in postgresql trino mysql mariadb tsql oracle clickhouse sqlite flightsql-generic; do \
		scripts/gen-dialect-lists.sh $$d || exit 1; \
	done

k8s-down:
	helm -n $(K8S_NS) uninstall baleia || true
	kubectl delete -k deploy/dev || true
	if [ -n "$${K8S_PURGE:-}" ]; then \
		minikube -p $(K8S_PROFILE) delete; \
	fi
