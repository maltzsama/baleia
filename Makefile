.PHONY: dev worker migrate sqlc lint build

dev:
	go run ./cmd/api

worker:
	go run ./cmd/worker

migrate:
	@echo "Running migrations..."
	# goose ou migrate CLI aqui

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
