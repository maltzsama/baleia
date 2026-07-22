BALEIA_DIR ?= $(CURDIR)
PLUGIN_DIR ?= $(abspath $(BALEIA_DIR)/../baleia-trino-catalog-store)
PLUGIN_REPO ?= https://github.com/maltzsama/baleia-trino-catalog-store.git
PLUGIN_TAG  ?= v0.1.0
PLUGIN_JAVA_IMAGE ?= eclipse-temurin:25-jdk

.PHONY: dev worker migrate sqlc lint build seed-tpch plugin-clone plugin-build clean-plugin up down logs restart-trino

dev:
	SMTP_HOST=localhost SMTP_PORT=1025 SMTP_FROM=noreply@baleia.io PORT=9000 go run ./cmd/api

worker:
	go run ./cmd/worker

# ── Plugin Trino ──────────────────────────────────────────────────────────────
# Clona e compila o baleia-trino-catalog-store usando Docker.
# O resultado vai para $(PLUGIN_DIR)/target/baleia-catalog-store/ e é consumido
# pelo Trino no compose via bind mount (docker-compose.yaml).
.PHONY: plugin-clone
plugin-clone:
	@if [ ! -d "$(PLUGIN_DIR)" ]; then \
		echo ">>> Clonando plugin $(PLUGIN_TAG) de $(PLUGIN_REPO)..."; \
		git clone --depth 1 --branch $(PLUGIN_TAG) $(PLUGIN_REPO) $(PLUGIN_DIR); \
	else \
		echo ">>> Plugin já clonado em $(PLUGIN_DIR)"; \
	fi

.PHONY: plugin-build
plugin-build: plugin-clone
	@echo ">>> Compilando plugin em $(PLUGIN_DIR)..."
	docker run --rm \
		-v "$(PLUGIN_DIR):/plugin" \
		-w /plugin \
		$(PLUGIN_JAVA_IMAGE) \
		sh -c 'apt-get update -qq && apt-get install -y -qq maven 2>/dev/null && mvn clean package -q -DskipTests'
	@echo ">>> Plugin compilado: $$(ls $(PLUGIN_DIR)/target/baleia-catalog-store/*.jar 2>/dev/null | head -1)"

.PHONY: clean-plugin
clean-plugin:
	rm -rf $(PLUGIN_DIR)

# ── Compose ───────────────────────────────────────────────────────────────────

# up: compila o plugin e sobe o stack completo.
up: plugin-build
	docker compose up -d

# down: derruba o stack sem destruir volumes (dados persistem).
down:
	docker compose down

# down-v: derruba o stack e remove volumes nomeados + containers parados +
# volumes órfãos. CUIDADO: destrói dados do postgres, temporal, etc.
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

# Insere a linha de teste tpch_teste em trino_catalog_registry, usada pelos
# testes de aceitação T1-T9 do plugin baleia-trino-catalog-store. Idempotente.
seed-tpch:
	docker compose exec -T postgres psql -U baleia -d baleia < db/seed/tpch_teste.sql

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
