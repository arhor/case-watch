# syntax=docker/dockerfile:1
FROM rust:1.100.0-alpine3.24 AS build
RUN apk add --no-cache build-base cmake clang file
WORKDIR /source
COPY experiments/rust/ ./
COPY internal/clients/testdata/ /internal/clients/testdata/
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/source/target \
    cargo fmt --check && \
    cargo clippy --locked --all-targets --target aarch64-unknown-linux-musl -- -D warnings && \
    cargo test --locked --all-targets --target aarch64-unknown-linux-musl && \
    cargo build --locked --release --target aarch64-unknown-linux-musl && \
    cp target/aarch64-unknown-linux-musl/release/case-watch-rust /case-watch-rust && \
    file /case-watch-rust

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY certificates/*.crt /usr/local/share/ca-certificates/
RUN update-ca-certificates
COPY --from=build /case-watch-rust /usr/local/bin/case-watch-rust
CMD ["case-watch-rust"]
