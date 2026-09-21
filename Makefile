.PHONY: build test lint linux-amd64 linux-arm64

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/xray-monitor ./cmd/xray-monitor

test:
	go test ./...

lint:
	go vet ./...

linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/xray-monitor-linux-amd64 ./cmd/xray-monitor

linux-arm64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/xray-monitor-linux-arm64 ./cmd/xray-monitor
