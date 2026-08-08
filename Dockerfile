# syntax=docker/dockerfile:1

ARG GO_IMAGE=golang:1.23-alpine@sha256:383395b794dffa5b53012a212365d40c8e37109a626ca30d6151c8348d380b5f
ARG DISTROLESS_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src

COPY go.mod ./
COPY main.go ./
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X github.com/kavrynt/kavryctl/internal/cli.Version=${VERSION} -X github.com/kavrynt/kavryctl/internal/cli.Commit=${COMMIT} -X github.com/kavrynt/kavryctl/internal/cli.BuildDate=${BUILD_DATE}" \
    -o /out/kavryctl .

FROM ${DISTROLESS_IMAGE}

ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="kavryctl" \
      org.opencontainers.image.description="Kavrynt command-line interface" \
      org.opencontainers.image.url="https://kavrynt.com" \
      org.opencontainers.image.source="https://github.com/kavrynt/kavryctl" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /out/kavryctl /usr/local/bin/kavryctl

USER nonroot:nonroot
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/kavryctl"]
CMD ["version"]
