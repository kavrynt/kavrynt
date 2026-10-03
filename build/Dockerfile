# syntax=docker/dockerfile:1.7
#
# Builds every Kavrynt runtime image from the repository root.
# Select the image with --target: gateway or operator.

ARG GO_IMAGE=golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468
ARG DISTROLESS_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS source

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal

FROM source AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    for component in gateway operator; do \
      pkg="github.com/kavrynt/kavrynt/internal/${component}/build"; \
      CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        -trimpath \
        -ldflags="-s -w -X ${pkg}.Version=${VERSION} -X ${pkg}.Commit=${COMMIT} -X ${pkg}.BuildDate=${BUILD_DATE}" \
        -o "/out/${component}" "./cmd/${component}"; \
    done

FROM ${DISTROLESS_IMAGE} AS runtime-base

ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.vendor="Kavrynt" \
      org.opencontainers.image.url="https://kavrynt.com" \
      org.opencontainers.image.source="https://github.com/kavrynt/kavrynt" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

USER nonroot:nonroot

FROM runtime-base AS gateway

LABEL org.opencontainers.image.title="gateway" \
      org.opencontainers.image.description="Kavrynt Gateway service"

COPY --from=build /out/gateway /usr/local/bin/gateway
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway"]

FROM runtime-base AS operator

LABEL org.opencontainers.image.title="k8s-operator" \
      org.opencontainers.image.description="Kavrynt Kubernetes Operator"

COPY --from=build /out/operator /usr/local/bin/manager
ENTRYPOINT ["/usr/local/bin/manager"]
