# 0010. Development environments

- Status: Proposed (owner decision D2, pending the owner's confirmation on #9)
- Date: 2026-10-14
- Issues: #9, #64, #88

## Context

Compose is the fastest way to evaluate Baleia, but production deployments run on Kubernetes. Bugs in
the chart (Trino region detection, the catalog guard not activated, missing environment variables)
were found only when someone deployed it, because development happened on compose.

## Decision

- **Compose stays** as the self-hosting quickstart for evaluators and contributors without
  Kubernetes.
- **minikube with the Helm chart** is the development environment for parity with production
  (`make k8s-up`, `deploy/dev/values-dev.yaml`).
- Both use the official Trino image with the plugin installed by an init container
  ([0004](0004-plugin-distribution.md)).

Pending: this ADR is `Proposed` because the owner has not confirmed D2 on #9. The text above is the
default. The chart and minikube environment already exist (#64), so accepting it records current
practice.

## Consequences

- Chart regressions show up during development, not at deployment.
- Two environments must be kept working; CI lints and renders the chart.

## Current compliance

Both environments exist. Compose still builds the plugin from source until #88.

## Alternatives considered

- **Compose only.** Rejected: chart bugs reach users.
- **Kubernetes only.** Rejected: too heavy for a quick evaluation.
