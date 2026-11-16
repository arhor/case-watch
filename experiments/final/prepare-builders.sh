#!/usr/bin/env bash
set -euo pipefail
docker build --target build -t case-watch-bench-go-builder -f Dockerfile .
docker build --target build -t case-watch-bench-rust-builder -f experiments/rust/Dockerfile .
docker build --target build -t case-watch-bench-kotlin-builder -f experiments/kotlin/Dockerfile .
docker build --target verify -t case-watch-bench-node-builder -f experiments/nodejs/Dockerfile .
