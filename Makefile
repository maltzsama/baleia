BALEIA_DIR ?= $(CURDIR)
PLUGIN_DIR ?= $(BALEIA_DIR)/.plugin/baleia-trino-catalog-store
PLUGIN_REPO ?= https://github.com/maltzsama/baleia-trino-catalog-store.git
PLUGIN_TAG  ?= v0.6.1
PLUGIN_JAVA_IMAGE ?= eclipse-temurin:25-jdk

# Fallback secret for local development. An exported or command-line
# JWT_SECRET wins, so credentials encrypted under a custom key stay readable.
JWT_SECRET ?= dev-secret-not-for-production

.PHONY: dev worker migrate sqlc lint build seed-tpch plugin-clone plugin-build clean-plugin up down logs restart-trino

dev:
	JWT_SECRET=$(JWT_SECRET) SMTP_HOST=localhost SMTP_PORT=1025 SMTP_FROM=noreply@baleia.io PORT=9000 go run ./cmd/api

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
