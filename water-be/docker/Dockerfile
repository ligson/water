# syntax=docker/dockerfile:1.7

ARG GO_BASE=golang:1.26-bookworm
ARG RUNTIME_BASE=node:22-bookworm-slim

FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS frontend-build

WORKDIR /src/water-fe

COPY water-fe/package.json water-fe/package-lock.json ./
RUN npm ci

COPY water-fe/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ${GO_BASE} AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

COPY water-be/go.mod water-be/go.sum ./
RUN go mod download

COPY water-be/ ./
COPY --from=frontend-build /src/water-fe/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.buildVersion=${VERSION}" \
    -o /out/water \
    ./cmd/water

FROM ${GO_BASE} AS go-runtime

FROM ${RUNTIME_BASE}

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        build-essential \
        ca-certificates \
        curl \
        git \
        jq \
        openssh-client \
        procps \
        python3 \
        python3-pip \
        python3-venv \
        ripgrep \
        tzdata \
        unzip \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go-runtime /usr/local/go /usr/local/go
COPY --from=build /out/water /usr/local/bin/water
RUN ln -s /usr/local/go/bin/go /usr/local/bin/go \
    && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

ENV PATH="/usr/local/go/bin:${PATH}" \
    WATER_HTTP_ADDR=":8080" \
    WATER_DATA_DIR="/data" \
    WATER_DATABASE_PATH="/data/water.db"

WORKDIR /workspace

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/water"]
