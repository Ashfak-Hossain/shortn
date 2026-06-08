.PHONY: run test lint docker

run:
	go run ./cmd/api

test:
	go test ./...

lint:
	golangci-lint run

docker:
	docker build --build-arg SERVICE=api -t shortn-api:dev .
