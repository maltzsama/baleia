BALEIA_DIR ?= $(CURDIR)
PLUGIN_DIR ?= $(abspath $(BALEIA_DIR)/../baleia-trino-catalog-store)
PLUGIN_JAVA_IMAGE ?= eclipse-temurin:25-jdk

.PHONY: dev worker migrate sqlc lint build seed-tpch plugin-build up down logs

dev:
	SMTP_HOST=localhost SMTP_PORT=1025 SMTP_FROM=noreply@baleia.io PORT=9000 go run ./cmd/api

worker:
	go run ./cmd/worker

# ── Plugin Trino ──────────────────────────────────────────────────────────────
# Compila o baleia-trino-catalog-store no repositório irmão usando Docker
# (JDK 25 + Maven instalado no container). O resultado vai para
# $(PLUGIN_DIR)/target/baleia-catalog-store/ e é consumido pelo Trino no compose
# via bind mount (docker-compose.yaml).
.PHONY: plugin-build
plugin-build:
	@if [ ! -d "$(PLUGIN_DIR)" ]; then \
		echo "ERRO: repositório do plugin não encontrado em $(PLUGIN_DIR)"; \
		echo "Clone o repositório irmão:"; \
		echo "  git clone git@github.com:maltzsama/baleia-trino-catalog-store.git $(PLUGIN_DIR)"; \
		exit 1; \
	fi
	@echo ">>> Compilando plugin em $(PLUGIN_DIR)..."
	docker run --rm \
		-v "$(PLUGIN_DIR):/plugin" \
		-w /plugin \
		$(PLUGIN_JAVA_IMAGE) \
		sh -c 'apt-get update -qq && apt-get install -y -qq maven 2>/dev/null && mvn clean package -q -DskipTests'
	@echo ">>> Plugin compilado: $$(ls $(PLUGIN_DIR)/target/baleia-catalog-store/*.jar 2>/dev/null | head -1)"

# ── Compose ───────────────────────────────────────────────────────────────────

# up: compila o plugin e sobe o stack completo.
up: plugin-build
	docker compose up -d

down:
	docker compose down

down-v:
	docker compose down -v

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
