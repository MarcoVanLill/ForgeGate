# syntax=docker/dockerfile:1.7
FROM golang:1.27.2-bookworm AS build
ARG BINARY=gateway
ENV GOMAXPROCS=4
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forgegate ./cmd/${BINARY}

FROM build AS race-test
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go test -race ./...

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && addgroup -S forgegate && adduser -S -G forgegate -u 10001 forgegate
WORKDIR /app
COPY --from=build --chown=forgegate:forgegate /out/forgegate /app/forgegate
USER 10001:10001
EXPOSE 8080 9090
ENTRYPOINT ["/app/forgegate"]
