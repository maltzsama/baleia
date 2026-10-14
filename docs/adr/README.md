# Architecture decision records

The ADRs in this directory are the current architectural truth for Baleia. CR documents remain the
change history; when an ADR and a CR disagree, the ADR wins.

Format: [MADR](https://adr.github.io/madr/), one decision per file, numbered in order. Start from
[`template.md`](template.md).

## Index

| ADR | Title | Status |
|---|---|---|
| [0001](0001-control-plane-only.md) | Baleia is a control plane, never a data plane | Accepted |
| [0002](0002-connections-engines-catalogs-bindings.md) | Connections, engines, catalogs and bindings | Accepted |
| [0003](0003-trino-binding.md) | How a catalog reaches Trino (the Trino binder) | Accepted |
| [0004](0004-plugin-distribution.md) | Catalog-store plugin distribution | Accepted |
| [0005](0005-trino-security-model.md) | Trino security model | Accepted |
| [0006](0006-multi-engine-scope.md) | Multi-engine scope | Accepted |
| [0007](0007-dispatch-and-wiring.md) | Adapters are constructed only at the wiring sites | Accepted |
| [0008](0008-error-and-secret-boundaries.md) | Error and secret boundaries | Accepted |
| [0009](0009-recurring-jobs.md) | Recurring jobs run on Temporal Schedules | Accepted |
| [0010](0010-dev-environments.md) | Development environments | Proposed |
| [0011](0011-direct-connections-security.md) | Security of direct connections to user-supplied hosts | Accepted |
| [0012](0012-semantic-layer-roadmap.md) | Semantic layer roadmap (no dbt) | Accepted |
| [0013](0013-content-model.md) | Saved content model | Accepted |

## Owner decisions

The owner decisions referenced across the backlog and where each one is recorded:

| Decision | Summary | Recorded in |
|---|---|---|
| D1 (revised) | Baleia is multi-engine: federated engines and direct engines | [0006](0006-multi-engine-scope.md) |
| D2 | Compose stays as the quickstart; minikube/Helm for dev parity | [0010](0010-dev-environments.md) (Proposed) |
| D3 | Managed-only authority: adopted catalogs are reported, never mutated | [0003](0003-trino-binding.md) |
| D4 | Registry secrets: Phase A by default (env/file refs stored as references) | [0003](0003-trino-binding.md), #31 |
| D5 | Connection test topology: metadata, direct engine and `bind:<engine>` checks | [0011](0011-direct-connections-security.md), #12 |
| D6 | The workspace role matrix lives in #16 | #16 |
| D7 | Refresh token in an HttpOnly cookie; no tokens in URLs | #51, #55 |
| D8 | User SQL runs as a per-user Trino identity | [0005](0005-trino-security-model.md) |
| D9 | Semantic layer: simple metrics in v1, Lightdash-level in v2, no dbt | [0012](0012-semantic-layer-roadmap.md) |
| D10 | The four-concept model: connections, engines, catalogs, bindings | [0002](0002-connections-engines-catalogs-bindings.md) |
| D11 | v1 provider scope | [0002](0002-connections-engines-catalogs-bindings.md) |

## Superseding an ADR

Never edit the decision of an accepted ADR in place. To change it:

1. Add a new ADR with the next number and a `Supersedes: NNNN` line under its status.
2. Change the old ADR's status to `Superseded by NNNN` (the only edit an accepted ADR receives,
   besides its "Current compliance" section).
3. Update the index above.

Each ADR has a **Current compliance** section that says where the code does not follow the
decision yet and which issue fixes it. Update that section as those issues land.
