VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE ?= ghcr.io/dmdhrumilmistry/vaanarsena

.PHONY: build agents test lint image clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vaanarsena ./cmd/vaanarsena
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vaanarsena-agent ./cmd/vaanarsena-agent

agents:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vaanarsena-agent-linux-amd64 ./cmd/vaanarsena-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vaanarsena-agent-linux-arm64 ./cmd/vaanarsena-agent

test:
	go vet ./...
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)"

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

clean:
	rm -rf bin dist
