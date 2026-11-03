# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=arm64
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/case-watch/ ./cmd/case-watch/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/case-watch ./cmd/case-watch
RUN mkdir -p /runtime/data /runtime/keys /runtime/etc/ssl/certs \
    && chmod 0700 /runtime/data /runtime/keys \
    && chown 10001:10001 /runtime/data /runtime/keys \
    && printf 'casewatch:x:10001:10001:Case Watch:/:/sbin/nologin\n' > /runtime/etc/passwd \
    && printf 'casewatch:x:10001:\n' > /runtime/etc/group \
    && cp /etc/ssl/certs/ca-certificates.crt /runtime/etc/ssl/certs/

FROM scratch
COPY --from=build /runtime/ /
COPY --from=build /out/case-watch /case-watch
# Europe/Warsaw rules are embedded by internal/scheduler's time/tzdata import.
USER 10001:10001
ENTRYPOINT ["/case-watch"]
CMD ["--run"]
