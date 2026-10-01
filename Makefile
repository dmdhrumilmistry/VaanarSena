VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE ?= ghcr.io/dmdhrumilmistry/vaanarsena
CLI_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build agents cli test e2e lint image clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vaanarsena ./cmd/vaanarsena
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vaanarsena-agent ./cmd/vaanarsena-agent
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vsctl ./cmd/vsctl

agents:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vaanarsena-agent-linux-amd64 ./cmd/vaanarsena-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vaanarsena-agent-linux-arm64 ./cmd/vaanarsena-agent

# vsctl for every desktop and CI platform.
cli:
	for p in $(CLI_PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vsctl-$$os-$$arch$$ext ./cmd/vsctl || exit 1; \
	done

test:
	go vet ./...
	go test -race ./...

# End-to-end suite against a throwaway PostgreSQL container.
E2E_DB := postgres://vs:vs@localhost:55432/vs_test?sslmode=disable
e2e:
	docker rm -f vs-e2e-pg >/dev/null 2>&1 || true
	docker run -d --name vs-e2e-pg -e POSTGRES_USER=vs -e POSTGRES_PASSWORD=vs -e POSTGRES_DB=vs_test -p 55432:5432 postgres:17-alpine
	until docker exec vs-e2e-pg pg_isready -U vs >/dev/null 2>&1; do sleep 1; done; sleep 1
	VS_TEST_DATABASE_URL="$(E2E_DB)" go test -count=1 -v ./internal/e2e/; status=$$?; docker rm -f vs-e2e-pg >/dev/null; exit $$status

lint:
	test -z "$$(gofmt -l .)"

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

clean:
	rm -rf bin dist
