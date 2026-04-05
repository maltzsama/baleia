.PHONY: dev worker migrate sqlc lint build

dev:
	PORT=9000 go run ./cmd/api

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


# eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1aWQiOiJkZTJjZjRkMy03NzgwLTRjNjktOWZmYi03OGYzNTNhZjliZTkiLCJyb2xlIjoiYWRtaW4iLCJleHAiOjE3NzU1MDczMjEsImlhdCI6MTc3NTQyMDkyMX0.YCjPO4FJ-NSMQOeI9_cQXz4i-CqdzES6KdVetSk_zfY
