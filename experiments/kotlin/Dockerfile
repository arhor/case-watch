# syntax=docker/dockerfile:1
FROM eclipse-temurin:25-jdk-alpine@sha256:c7c3d0bdf7f2bbc5a05a6712ed334d198360105634ea9adf76fca94f381b1e3f AS build
WORKDIR /workspace/experiments/kotlin
COPY experiments/kotlin/ .
COPY internal/clients/testdata/ /workspace/internal/clients/testdata/
# Reuse downloads while source changes still rerun checks and compilation.
RUN --mount=type=cache,target=/root/.gradle \
    ./gradlew --no-daemon formatCheck check installDist distTar
RUN jdeps --ignore-missing-deps --multi-release 25 --print-module-deps \
      --class-path 'build/install/case-watch-kotlin/lib/*' build/libs/case-watch-kotlin.jar > /tmp/modules \
    && jlink --add-modules "$(cat /tmp/modules),jdk.crypto.ec" \
      --strip-debug --no-header-files --no-man-pages --compress=2 --output /opt/java-min
COPY certificates/*.crt /tmp/case-watch-certificates/
RUN for cert in /tmp/case-watch-certificates/*.crt; do \
      keytool -importcert -noprompt -trustcacerts \
        -keystore /opt/java-min/lib/security/cacerts -storepass changeit \
        -alias "case-watch-$(basename "$cert")" -file "$cert"; \
    done \
    && keytool -genkeypair -noprompt -alias probe -keyalg EC -groupname secp256r1 \
      -dname 'CN=localhost' -validity 1 -storetype PKCS12 \
      -keystore /tmp/probe.p12 -storepass synthetic-probe \
    && /opt/java-min/bin/java --enable-native-access=ALL-UNNAMED \
      -cp 'build/classes/kotlin/test:build/install/case-watch-kotlin/lib/*' \
      casewatch.RuntimeProbe /tmp/probe.p12 /tmp/case-watch-certificates

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf AS runtime
RUN apk add --no-cache libstdc++ zlib ca-certificates
COPY --from=build /opt/java-min /opt/java
COPY --from=build /workspace/experiments/kotlin/build/install/case-watch-kotlin /opt/case-watch
ENV JAVA_HOME=/opt/java
ENV PATH="/opt/java/bin:${PATH}"
ENTRYPOINT ["/opt/case-watch/bin/case-watch-kotlin"]

# Exercise the final runtime's native dependencies without shipping test artifacts.
FROM runtime AS verify
COPY --from=build /workspace/experiments/kotlin/build/classes/kotlin/test /tmp/probe-classes
COPY --from=build /tmp/probe.p12 /tmp/probe.p12
COPY --from=build /tmp/case-watch-certificates /tmp/probe-certificates
RUN java --enable-native-access=ALL-UNNAMED \
      -cp '/tmp/probe-classes:/opt/case-watch/lib/*' \
      casewatch.RuntimeProbe /tmp/probe.p12 /tmp/probe-certificates \
    && touch /verification-passed

FROM runtime AS final
COPY --from=verify /verification-passed /opt/java/.verification-passed
