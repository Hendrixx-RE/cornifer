.PHONY: up down migrate fetch-repo index query test lint fmt

CORNIFER_DATABASE_URL ?= postgres://cornifer:cornifer@localhost:5433/cornifer?sslmode=disable

up:
	docker compose up -d
	docker compose exec -T postgres sh -c 'until pg_isready -U cornifer -d cornifer; do sleep 1; done'

down:
	docker compose down

migrate:
	CORNIFER_DATABASE_URL="$(CORNIFER_DATABASE_URL)" go run ./cmd/migrate up

fetch-repo:
	./scripts/fetch-target-repo.sh

index:
	go run ./cmd/cornifer index

query:
	go run ./cmd/cornifer query "$(Q)"

test:
	go test ./...

lint:
	go vet ./...

fmt:
	gofmt -l -w .
