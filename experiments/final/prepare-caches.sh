#!/usr/bin/env bash
set -euo pipefail
for stack in go rust kotlin; do
 case "$stack" in
 go) line='RUN --mount=type=cache,target=/go/pkg/mod cp -a /go/pkg/mod /bench-mod';;
 rust) line='RUN --mount=type=cache,target=/usr/local/cargo/registry cp -a /usr/local/cargo/registry /bench-registry';;
 kotlin) line='RUN --mount=type=cache,target=/root/.gradle cp -a /root/.gradle /bench-gradle';;
 esac
 printf 'FROM case-watch-bench-%s-builder\n%s\n' "$stack" "$line" | docker build -t "case-watch-bench-$stack-toolchain" -f - .
done
