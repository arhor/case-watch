#!/usr/bin/env bash
set -euo pipefail
bench_root=$(pwd)
for stack in go rust kotlin; do
 name="case-watch-final-probe-build-$stack"
 docker run -d --name "$name" --network none --entrypoint sleep "case-watch-bench-$stack-toolchain" infinity
 case "$stack" in
 go)
 docker cp experiments/nodejs/measurements/go-probe "$name:/app/probe"
 docker exec "$name" sh -ec 'cp -a /bench-mod /tmp/mod; cd /app; CGO_ENABLED=0 GOMODCACHE=/tmp/mod GOCACHE=/tmp/cache go build -trimpath -ldflags="-s -w" -o /probe-bin ./probe'
 docker cp "$name:/probe-bin" experiments/final/go-probe-bin
 ;;
 rust)
 docker cp experiments/final/rust-main.rs "$name:/source/src/main.rs"
 docker exec "$name" sh -ec 'mkdir -p /usr/local/cargo/registry; cp -a /bench-registry/. /usr/local/cargo/registry/; cd /source; cargo build --offline --locked --release --target aarch64-unknown-linux-musl'
 docker cp "$name:/source/target/aarch64-unknown-linux-musl/release/case-watch-rust" experiments/final/rust-probe-bin
 ;;
 kotlin)
 docker cp experiments/final/kotlin-Main.kt "$name:/workspace/experiments/kotlin/src/main/kotlin/casewatch/Main.kt"
 docker exec "$name" sh -ec 'mkdir -p /root/.gradle; cp -a /bench-gradle/. /root/.gradle/; cd /workspace/experiments/kotlin; ./gradlew --offline --no-daemon jar'
 docker cp "$name:/workspace/experiments/kotlin/build/libs/case-watch-kotlin.jar" experiments/final/kotlin-probe.jar
 ;;
 esac
 docker rm -f "$name"
done
