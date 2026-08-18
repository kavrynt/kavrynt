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

RUN mkdir -p /out/data

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X github.com/kavrynt/registry/internal/build.Version=${VERSION} -X github.com/kavrynt/registry/internal/build.Commit=${COMMIT} -X github.com/kavrynt/registry/internal/build.BuildDate=${BUILD_DATE}" \
    -o /out/registry .

FROM ${DISTROLESS_IMAGE}

ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="registry" \
      org.opencontainers.image.description="Kavrynt Registry service" \
      org.opencontainers.image.url="https://kavrynt.com" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.vendor="Kavrynt"

COPY --from=build /out/registry /usr/local/bin/registry
COPY --from=build --chown=nonroot:nonroot /out/data /data

USER nonroot:nonroot
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/registry"]
CMD ["--addr", ":8080", "--data", "/data/registry.json"]
