# syntax=docker/dockerfile:1.7

FROM golang:1.23.4-alpine3.21 AS build

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/mcp-server .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/mcp-server /mcp-server

EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/mcp-server"]
