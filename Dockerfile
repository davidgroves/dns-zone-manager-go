# DNS Zone Manager (Go) — multi-stage image
# Builds the SPA, embeds it, and ships static binaries.

# -----------------------------------------------------------------------------
# Stage 1: frontend
# -----------------------------------------------------------------------------
FROM node:24-slim AS frontend

WORKDIR /app

COPY package.json package-lock.json* .npmrc ./
RUN npm ci

COPY tsconfig.json vite.config.ts biome.json ./
COPY frontend/ ./frontend/

RUN npx tsc && npx vite build

# -----------------------------------------------------------------------------
# Stage 2: Go build
# -----------------------------------------------------------------------------
FROM golang:1.27-bookworm AS builder

WORKDIR /src
ARG VERSION=v0.0.0+unknown

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /app/dist/ ./internal/ui/dist/

RUN CGO_ENABLED=0 go build \
      -ldflags "-X github.com/davidgroves/dns-zone-manager-go/internal/version.Version=${VERSION}" \
      -o /out/dns-zone-manager ./cmd/dns-zone-manager \
 && CGO_ENABLED=0 go build \
      -ldflags "-X github.com/davidgroves/dns-zone-manager-go/internal/version.Version=${VERSION}" \
      -o /out/dns-cli ./cmd/dns-cli

# -----------------------------------------------------------------------------
# Stage 3: runtime (distroless — no shell/wget for HEALTHCHECK)
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/dns-zone-manager /dns-zone-manager
COPY --from=builder /out/dns-cli /dns-cli

EXPOSE 8000
USER nonroot:nonroot

# Probe /health externally (compose/k8s); distroless has no curl/wget.
ENTRYPOINT ["/dns-zone-manager"]
CMD ["serve"]
