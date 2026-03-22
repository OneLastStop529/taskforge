.PHONY: help demo run build test fmt clean

BINARY := bin/taskforge

help:
	@echo "Available targets:"
	@echo "  make demo   - run the in-process demo"
	@echo "  make run    - alias for demo"
	@echo "  make build  - build the taskforge CLI into $(BINARY)"
	@echo "  make test   - run the Go test suite"
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

fmt:
	go fmt ./...

clean:
	rm -rf bin
