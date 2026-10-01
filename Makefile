.PHONY: build test test-race test-integration lint fmt tidy run frontend-build docker-build types-generate

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0+unknown)
LDFLAGS := -X github.com/davidgroves/dns-zone-manager-go/internal/version.Version=$(VERSION)

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/dns-zone-manager ./cmd/dns-zone-manager
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/dns-cli ./cmd/dns-cli

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	go test -tags=integration ./tests/integration/...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')
	go mod tidy

tidy:
	go mod tidy

run:
	go run ./cmd/dns-zone-manager serve --config examples/config.yaml

frontend-build:
	npm ci
	npm run build
	rm -rf internal/ui/dist
	mkdir -p internal/ui/dist
	cp -a dist/. internal/ui/dist/

docker-build:
	docker build -t dns-zone-manager:$(VERSION) .

types-generate:
	npm run types:generate
