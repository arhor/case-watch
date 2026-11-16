# Smaller Alpine image — 2026-11-16

The default Dockerfile now uses Alpine 3.24 and the official musl Node.js
24.22.3 executable. The final image contains Node, its runtime libraries,
CA certificates and the application. npm, Yarn, Prettier, tests and fixtures
remain in verification stages. This follows the official Node image project's
[smaller images without npm/Yarn guidance](https://github.com/nodejs/docker-node/blob/main/docs/BestPractices.md#smaller-images-without-npmyarn).

| ARM64 image          | Docker `.Size` bytes |    MiB |
| -------------------- | -------------------: | -----: |
| Previous Debian slim |          349,505,803 | 333.31 |
| Minimal Alpine       |          196,075,668 | 186.99 |
| Reduction            |          153,430,135 | 146.32 |

The optimized image is **43.9% smaller** using the same Docker image-inspect
metric as the previous report. These are logical image sizes including runtime
and base layers, not compressed registry download sizes or unique disk usage.
The Node executable alone is 128,247,648 bytes; this remains a full V8 runtime
with ICU and SQLite. No custom Node compilation or runtime feature removal is
applied.

## Build and verification

From repository root:

```sh
docker build --platform linux/arm64 -f experiments/nodejs/Dockerfile \
  -t case-watch-node:24.22.3-alpine .
docker image inspect case-watch-node:24.22.3 case-watch-node:24.22.3-alpine \
  --format '{{json .RepoTags}} {{.Size}} {{.Id}}'
```

The ARM64 build passed:

- Locked dependency installation, Prettier, JavaScript syntax checks and release
  packaging.
- All 35 tests in the Alpine verification stage.
- All 35 tests again against the minimal final runtime's native libraries.
- A runtime probe for SQLite, Warsaw winter/summer offsets, bundled CA roots,
  and the repository portal intermediate certificate in Node's extra trust store.
- Verification that npm is absent from the final image.

Runtime versions remain Node 24.22.3, SQLite 3.54.1 and ICU 78.3. Alpine uses
musl instead of Debian's glibc. No compatibility shim or native addon build is
needed by this application. Both base images are pinned by digest; Alpine
runtime packages are resolved from its 3.24 repository during builds.

Measured image IDs:

- Debian: `sha256:792db207b956ed55a359e90b069364dc591bc252c3d85bf2c843f10da94ae7d9`
- Alpine: `sha256:3fcc794b8e9ec40fe711675b062544234783b9ac61302180f70f59e0854765e3`

`compose.yaml` selects the `24.22.3-alpine` tag. The original Debian recipe is
retained as `Dockerfile.debian` with its own Docker ignore file; the previously
measured Debian image/tag was not overwritten. No container was deployed and
the live Go bot was untouched.

The existing [CPU/RSS report](REPORT.md) describes Debian. Those numbers have
not been remeasured for musl/Alpine; image size reduction does not establish a
corresponding RAM or CPU reduction.
