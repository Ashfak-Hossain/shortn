.PHONY: run test lint docker migrate-up migrate-down

DATABASE_URL ?= postgres://dev:dev@localhost:5432/shortn?sslmode=disable

run:
	go run ./cmd/api

test:
	go test ./...

lint:
	golangci-lint run

docker:
	docker build --build-arg SERVICE=api -t shortn-api:dev .

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1
