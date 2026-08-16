# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X github.com/kavrynt/k8s-operator/internal/build.Version=${VERSION} -X github.com/kavrynt/k8s-operator/internal/build.Commit=${COMMIT} -X github.com/kavrynt/k8s-operator/internal/build.BuildDate=${BUILD_DATE}" \
    -o /out/manager .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/manager /usr/local/bin/manager

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/manager"]
