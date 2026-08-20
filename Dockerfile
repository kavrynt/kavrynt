# syntax=docker/dockerfile:1

ARG GO_IMAGE=golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468
ARG DISTROLESS_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:1b7b9f0f0e0a1d2155f531db587cc48ec26aaf97ab64364225f5bf18a054e66a

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.1-beta
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src

COPY go.mod ./
COPY main.go ./
COPY internal ./internal

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X github.com/kavrynt/kavryctl/internal/cli.Version=${VERSION} -X github.com/kavrynt/kavryctl/internal/cli.Commit=${COMMIT} -X github.com/kavrynt/kavryctl/internal/cli.BuildDate=${BUILD_DATE}" \
    -o /out/kavryctl .

FROM ${DISTROLESS_IMAGE}

ARG VERSION=0.0.1-beta
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="kavryctl" \
      org.opencontainers.image.description="Kavrynt command-line interface" \
      org.opencontainers.image.url="https://kavrynt.com" \
      org.opencontainers.image.source="https://github.com/kavrynt/kavryctl" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.vendor="Kavrynt"

COPY --from=build /out/kavryctl /usr/local/bin/kavryctl

USER nonroot:nonroot
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/kavryctl"]
CMD ["version"]
