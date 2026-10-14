# 0004. Catalog-store plugin distribution

- Status: Accepted
- Date: 2026-10-14
- Issues: #9, #64, #88

## Context

The `baleia-trino-catalog-store` plugin is a Java artifact built against a specific Trino SPI.
Building it from source needs a JDK, and shipping a custom Trino image would make Baleia
responsible for Trino's security updates.

## Decision

- The plugin is released as a versioned tarball
  `baleia-catalog-store-<version>+trino<tag>.tar.gz` with a `.sha256` file.
- It is installed by an **init container** into the **official** `trinodb/trino:<tag>` image. Baleia
  never builds or publishes a Trino image. An optional small image that only contains the plugin
  directory is allowed for air-gapped installs.
- The Trino tag is pinned to the SPI the plugin was built for.
- Upgrading Trino follows the plugin's upgrade procedure (the plugin's `docs/spi-<tag>.md`
  inventory), then a plugin release, then the pin bump in Baleia.

## Consequences

- Starting the stack needs no JDK.
- Operators with an existing Trino install the same tarball.
- A Trino upgrade is never a Baleia-only change.

## Current compliance

Partially compliant.

- The Helm chart installs the plugin from the release tarball with a sha256 check, or from a
  plugin-only image (`deploy/helm/baleia/templates/trino.yaml`).
- Compose and the `Makefile` still clone and build the plugin from source (`PLUGIN_TAG`), and
  `trino_catalog/` remains in the repository (#88).

## Alternatives considered

- **Custom Trino image with the plugin baked in.** Rejected: Baleia would own Trino's image
  updates and CVEs.
- **Build from source at startup.** Rejected: requires a JDK and is slow and fragile.
