.PHONY: all build test vet tidy run clean

GO ?= go
BIN_DIR := bin
BINARY := $(BIN_DIR)/nanobot

all: vet test build

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BINARY) ./cmd/nanobot

run: build
	$(BINARY)

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR)
