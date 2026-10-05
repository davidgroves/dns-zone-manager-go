.PHONY: build test test-race test-integration lint fmt tidy run frontend-build docker-build devcontainer-image types-generate

DEVCONTAINER_IMAGE ?= ghcr.io/davidgroves/dns-zone-manager-devcontainer:latest
# Platforms for the multi-arch devcontainer image (Apple Silicon + Intel/Linux).
DEVCONTAINER_PLATFORMS ?= linux/amd64,linux/arm64

VERSION ?= $(shell ./scripts/app-version.sh)
LDFLAGS := -X github.com/davidgroves/dns-zone-manager-go/internal/version.Version=$(VERSION)
# Docker tags cannot contain '+'; keep the real VERSION for ldflags/build-arg.
DOCKER_TAG := $(shell printf '%s' '$(VERSION)' | tr '+' '-')

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/dns-zone-manager ./cmd/dns-zone-manager
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/dns-cli ./cmd/dns-cli
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/dns-tests ./cmd/dns-tests
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/dns-perf ./cmd/dns-perf

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
	docker build --build-arg VERSION=$(VERSION) -t dns-zone-manager:$(DOCKER_TAG) .

# Build and push the multi-arch Dev Container image to GHCR. CI normally does
# this (.github/workflows/devcontainer-image.yml); use this for a manual publish
# after editing .devcontainer/Dockerfile. Requires `docker login ghcr.io`.
devcontainer-image:
	docker buildx build --platform $(DEVCONTAINER_PLATFORMS) \
		-f .devcontainer/Dockerfile \
		-t $(DEVCONTAINER_IMAGE) --push .

types-generate:
	npm run types:generate
