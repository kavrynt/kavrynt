# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build

ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X github.com/kavrynt/k8s-operator/internal/build.Version=${VERSION} -X github.com/kavrynt/k8s-operator/internal/build.Commit=${COMMIT} -X github.com/kavrynt/k8s-operator/internal/build.BuildDate=${BUILD_DATE}" \
    -o /out/manager .

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=0.1.0-dev
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
