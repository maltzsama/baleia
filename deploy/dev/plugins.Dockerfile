# Bundles the RELEASED catalog-store plugin (baleia-trino-catalog-store v2.0)
# into a tiny sidecar image: downloads the release tarball at build time and
# verifies its sha256, so the image always carries the same verified plugin the
# chart's initContainer expects. Trino itself always runs the official
# trinodb/trino image; this image just ships the plugin files at
# /baleia-catalog-store.
FROM alpine:3.20
ARG K8S_PLUGIN_VERSION=0.9.0
RUN apk add --no-cache wget \
 && wget -qO /tmp/p.tgz https://github.com/maltzsama/baleia-trino-catalog-store/releases/download/v${K8S_PLUGIN_VERSION}/baleia-catalog-store-${K8S_PLUGIN_VERSION}+trino483.tar.gz \
 && wget -qO /tmp/p.sha256 https://github.com/maltzsama/baleia-trino-catalog-store/releases/download/v${K8S_PLUGIN_VERSION}/baleia-catalog-store-${K8S_PLUGIN_VERSION}+trino483.tar.gz.sha256 \
 && echo "$(cat /tmp/p.sha256)  /tmp/p.tgz" | sha256sum -c - \
 && mkdir -p /tmp/x && tar -xzf /tmp/p.tgz -C /tmp/x \
 && test "$(cat /tmp/x/VERSION)" = "${K8S_PLUGIN_VERSION}+trino483" \
 && mv /tmp/x/baleia-catalog-store / && rm -rf /tmp/x \
 && rm -rf /tmp/p.tgz /tmp/p.sha256