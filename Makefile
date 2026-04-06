.PHONY: dev worker migrate sqlc lint build

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

sqlc:
	sqlc generate

lint:
	golangci-lint run ./...

build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

compose-up:
	docker compose up -d

compose-down:
	docker compose down

generate:
	oapi-codegen --config=config/oapi-models.yaml api/swagger.yaml
	oapi-codegen --config=config/oapi-server.yaml api/swagger.yaml
