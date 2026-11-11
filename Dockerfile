# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /case-watch .

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /case-watch /case-watch

ENTRYPOINT ["/case-watch"]
