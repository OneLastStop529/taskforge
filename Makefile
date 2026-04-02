.PHONY: help demo run build test test-redis redis-up redis-down fmt clean

BINARY := bin/taskforge

help:
	@echo "Available targets:"
	@echo "  make demo   - run the in-process demo"
	@echo "  make run    - alias for demo"
	@echo "  make build  - build the taskforge CLI into $(BINARY)"
	@echo "  make test   - run the Go test suite"
	@echo "  make test-redis - run the Redis integration tests"
	@echo "  make redis-up - start local Redis via docker compose"
	@echo "  make redis-down - stop local Redis via docker compose"
	@echo "  make fmt    - format Go source files"
	@echo "  make clean  - remove built artifacts"

demo:
	go run ./cmd/taskforge demo

run: demo

build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/taskforge

test:
	go test ./...

test-redis:
	go test ./pkg/taskforge -run RedisIntegration -v

redis-up:
	docker compose up -d redis

redis-down:
	docker compose down

fmt:
	go fmt ./...

clean:
	rm -rf bin
