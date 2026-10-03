.PHONY: all build run test test-race test-cover lint clean docker-build docker-up docker-down

BINARY_NAME=bin/server
MAIN_PACKAGE=./cmd/server

all: build test

build:
	@echo "==> Building binary..."
	@mkdir -p bin
	go build -ldflags="-s -w" -o $(BINARY_NAME) $(MAIN_PACKAGE)

run: build
	@echo "==> Running server locally..."
	./$(BINARY_NAME)

test:
	@echo "==> Running all tests..."
	go test -v ./...

test-race:
	@echo "==> Running tests with race detector..."
	go test -v -race ./...

test-cover:
	@echo "==> Running tests with coverage..."
	go test -cover -race ./...

lint:
	@echo "==> Running go vet..."
	go vet ./...

clean:
	@echo "==> Cleaning build artifacts..."
	rm -rf bin/ tmp/ *.out

docker-build:
	@echo "==> Building Docker image..."
	docker compose build

docker-up:
	@echo "==> Starting application and PostgreSQL in Docker..."
	docker compose up --build -d

docker-down:
	@echo "==> Stopping Docker containers..."
	docker compose down -v
