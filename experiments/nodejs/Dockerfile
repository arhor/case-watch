# syntax=docker/dockerfile:1
FROM node:24.22.3-alpine3.24@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5 AS verify
WORKDIR /workspace/experiments/nodejs
COPY experiments/nodejs/package*.json ./
RUN npm ci --ignore-scripts
COPY experiments/nodejs/ ./
COPY internal/clients/testdata/ /workspace/internal/clients/testdata/
RUN npm run format:check && npm run check && npm test && npm run build

# Keep package managers in verification; ship just Node and its runtime libraries.
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf AS runtime
RUN apk add --no-cache libstdc++ libatomic ca-certificates
COPY --from=verify /usr/local/bin/node /usr/local/bin/node
WORKDIR /app
COPY --from=verify /workspace/experiments/nodejs/src/ ./src/
COPY --from=verify /workspace/experiments/nodejs/migrations/ ./migrations/
COPY --from=verify /workspace/experiments/nodejs/package.json ./
COPY certificates/*.crt /usr/local/share/ca-certificates/
ENV NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/certum-dv-tls-g2-r39-ca.crt
CMD ["node", "src/main.js"]

# Run the full suite against the actual minimal runtime, including native libraries.
FROM runtime AS runtime-verify
COPY --from=verify /workspace/experiments/nodejs/test/ /app/test/
COPY --from=verify /workspace/internal/clients/testdata/ /internal/clients/testdata/
COPY --from=verify /workspace/experiments/nodejs/scripts/runtime-probe.js /tmp/runtime-probe.js
RUN node /tmp/runtime-probe.js && node --test --test-timeout=15000 test/*.test.js \
    && test ! -e /usr/local/bin/npm && touch /verification-passed

FROM runtime AS final
COPY --from=runtime-verify /verification-passed /app/.verification-passed
