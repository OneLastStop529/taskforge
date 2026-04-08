.PHONY: help demo run build install test test-redis redis-up redis-down fmt clean zsh-completion

BINARY := bin/taskforge

help:
	@echo "Available targets:"
	@echo "  make demo   - run the in-process demo"
	@echo "  make run    - alias for demo"
	@echo "  make build  - build the taskforge CLI into $(BINARY)"
	@echo "  make install - install the taskforge CLI into GOBIN or GOPATH/bin"
	@echo "  make zsh-completion - print repo-local zsh completion instructions"
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

install:
	go install ./cmd/taskforge

zsh-completion:
	@echo "Run this from a zsh shell started in the repo:"
	@echo "  source ./scripts/taskforge-completion.zsh"
	@echo
	@echo "This loads taskforge completions for the current shell only."

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
