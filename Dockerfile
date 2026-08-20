# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS build

ARG VERSION=0.0.1-beta
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X github.com/kavrynt/k8s-operator/internal/build.Version=${VERSION} -X github.com/kavrynt/k8s-operator/internal/build.Commit=${COMMIT} -X github.com/kavrynt/k8s-operator/internal/build.BuildDate=${BUILD_DATE}" \
    -o /out/manager .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:1b7b9f0f0e0a1d2155f531db587cc48ec26aaf97ab64364225f5bf18a054e66a

ARG VERSION=0.0.1-beta
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="k8s-operator" \
      org.opencontainers.image.description="Kavrynt Kubernetes Operator" \
      org.opencontainers.image.vendor="Kavrynt" \
      org.opencontainers.image.url="https://kavrynt.com" \
      org.opencontainers.image.source="https://github.com/kavrynt/operator" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

COPY --from=build /out/manager /usr/local/bin/manager

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/manager"]
