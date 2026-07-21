.PHONY: dev worker migrate sqlc lint build seed-tpch

dev:
	SMTP_HOST=localhost SMTP_PORT=1025 SMTP_FROM=noreply@baleia.io PORT=9000 go run ./cmd/api


worker:
	go run ./cmd/worker

migrate:
	go run ./cmd/migrate -cmd=up

migrate-down:
	go run ./cmd/migrate -cmd=down

migrate-status:
	go run ./cmd/migrate -cmd=status

migrate-reset:
	go run ./cmd/migrate -cmd=reset

# Insere a linha de teste tpch_teste em trino_catalog_registry, usada pelos
# testes de aceitação T1-T9 do plugin baleia-trino-catalog-store. Idempotente.
seed-tpch:
	docker compose exec -T postgres psql -U baleia -d baleia < db/seed/tpch_teste.sql

sqlc:
	sqlc generate

lint:
	golangci-lint run ./...

build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

build-web:
	cd web && npm run build

build-all: build-web build

compose-up:
	docker compose build --no-cache && docker compose up -d

compose-down:
	docker compose rm -fsv
	docker compose down -v
	docker container prune -f
	docker volume prune -f

compose-logs:
	docker compose logs -f api worker

generate:
	oapi-codegen --config=config/oapi-models.yaml api/swagger.yaml
	oapi-codegen --config=config/oapi-server.yaml api/swagger.yaml
