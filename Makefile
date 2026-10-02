GOPATH ?= $(shell go env GOPATH)
export PATH := $(GOPATH)/bin:$(PATH)

BINARY_NAME := herd
BIN_DIR := bin

PREFIX ?= $(HOME)/.local
INSTALL_BIN ?= $(PREFIX)/bin

.PHONY: all build test lint clean vet check install

all: check build

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/herd

install: build
	mkdir -p $(INSTALL_BIN)
	cp $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_BIN)/$(BINARY_NAME)
	@echo "Installed $(BINARY_NAME) to $(INSTALL_BIN)/$(BINARY_NAME)"

test:
	go test -v -race ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

build-all:
	mkdir -p $(BIN_DIR)/release
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN_DIR)/release/$(BINARY_NAME)-linux-amd64 ./cmd/herd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(BIN_DIR)/release/$(BINARY_NAME)-linux-arm64 ./cmd/herd
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN_DIR)/release/$(BINARY_NAME)-darwin-amd64 ./cmd/herd
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o $(BIN_DIR)/release/$(BINARY_NAME)-darwin-arm64 ./cmd/herd
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN_DIR)/release/$(BINARY_NAME)-windows-amd64.exe ./cmd/herd

clean:
	rm -rf $(BIN_DIR) dist/ *.out coverage.txt

check: vet lint test

