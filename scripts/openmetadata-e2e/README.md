# OpenMetadata through the Baleia BI gateway

This harness validates how OpenMetadata discovers Baleia (#1122, #1135):

- OpenMetadata's stock **Trino** connector ingests the seeded physical tables
  through a workspace-scoped Baleia BI gateway, with the workspace BI key.
- The gateway's **semantic catalog routes**
  ([ADR 0030](../../docs/adr/0030-semantic-catalog-through-the-bi-gateway.md))
  return the seeded model and metric with the same key, and every physical
  table they name is a table OpenMetadata ingested. That name match is what the
  [`baleia-openmetadata`](https://github.com/maltzsama/baleia-openmetadata)
  connector relies on for lineage.
- The **Baleia connector** runs from OpenMetadata's ingestion image with
  `baleia-openmetadata` installed (#1137). With the same BI key, read from
  the environment and never stored in OpenMetadata, it creates the
  `e2e_bi_orders` data model and the seeded metrics, with lineage from the
  `orders` table the Trino connector ingested. A throwaway model created
  through Baleia's API and then deleted must be soft-deleted by the next run.

Baleia never calls OpenMetadata and stores no OpenMetadata credential.

The harness supports OpenMetadata **2.0.9** and **2.1.0**, each pinned by the
SHA-256 of its official quickstart compose file. The ingestion image and the
server come from the same release, as OpenMetadata requires. `make
e2e-openmetadata` runs every release in `OPENMETADATA_VERSIONS` (default
`2.0.9 2.1.0`) one after the other; set `OPENMETADATA_VERSION` and
`OPENMETADATA_COMPOSE_SHA256` to run the script against another release.

`BALEIA_OPENMETADATA` is the pip requirement installed into the ingestion
image: `baleia-openmetadata==0.1.*` by default, a local wheel path to test an
unreleased connector, or `skip` to leave the connector out. The throwaway model
is created and deleted as `admin@e2e.test` through the API forwarded on
`localhost:8080` by `make e2e-seed` (`BALEIA_API_URL`, `BALEIA_ADMIN_EMAIL`,
`BALEIA_ADMIN_PASSWORD`).

## Run

The Baleia minikube environment must be running and seeded first. The normal
setup is:

```sh
make e2e            # or: make k8s-up && make e2e-seed
make e2e-openmetadata
make e2e-down
```

`make e2e` leaves the cluster running after its checks. The seed writes the
workspace BI key to `/tmp/baleia-e2e-bi-key.secret`; set `BI_E2E_KEY_FILE` when
using another path.

`make e2e-openmetadata` is intentionally not called by `make e2e` or the
ordinary PR CI path. The OpenMetadata quickstart is a heavyweight external
integration dependency, so the target runs manually and in the nightly
`openmetadata-e2e` workflow, one release per runner, which opens a `Nightly
failure` issue when a scheduled run fails.

The script starts and removes a temporary OpenMetadata quickstart, forwards
the Baleia BI gateway, logs in with the quickstart's default test admin, runs
the stock `metadata ingest` command, asserts the service and both tables through
the OpenMetadata API, reads the semantic catalog routes with the BI key, and
removes all OpenMetadata containers and volumes on exit. The local gateway hop is HTTP only inside the test; a deployed
integration must use TLS because the BI key is a password credential. The
OpenMetadata server binds its host ports `18585`/`18586` to loopback by default
so an existing
local quickstart on `8585` or Baleia's API on `8080` does not collide. The
temporary ingestion container has no published port and uses host networking
only for this local one-shot process, so it can reach the two host-published
test endpoints without Docker bridge-specific routing.

## Assertions and limitations

The workflow is scoped to the seeded `e2e_tpch.tiny.orders` and
`e2e_tpch.tiny.customer` tables. It proves that OpenMetadata can reach the
gateway with the workspace BI key and ingest physical metadata, including
columns, through the Trino protocol. The gateway still enforces the workspace
catalog scope and read-only policy on every request.

To verify a view against a source connector that exposes view definitions, set
the catalog/schema to that fixture and provide its view name:

```sh
OPENMETADATA_CATALOG=source_catalog \
OPENMETADATA_SCHEMA=analytics \
OPENMETADATA_VIEW_NAME=orders_view \
make e2e-openmetadata
```

The semantic catalog checks need the seeded `e2e_tpch.tiny` schema, so they
are skipped, with a message, when `OPENMETADATA_CATALOG` or
`OPENMETADATA_SCHEMA` selects another one. The selected fixture schema must
also contain the physical `orders` and
`customer` tables, with columns, in addition to `orders_view`; the harness
checks those two tables on every run. It then requires the OpenMetadata entity
to be a view and to contain a non-empty view definition. This is intentionally
opt-in: the seeded TPCH connector does not provide a mutable view fixture, and
a connector that does not expose view definitions cannot satisfy view-definition
discovery.

The preceding `make e2e` run also covers the negative gateway contract: an
out-of-scope catalog is denied and a disposable BI key is revoked and rejected
on its next request. Those checks stay in the protocol harness because they do
not require starting OpenMetadata.

The current Baleia gateway records connector introspection as `origin=bi`, so
those statements are visible in query history. This is intentional and is the
behavior that #1122 documents; changing history classification belongs in a
follow-up implementation decision, not in this test harness.

Views are enabled in the OpenMetadata workflow. View-definition discovery is
validated by the opt-in `OPENMETADATA_VIEW_NAME` assertion above when the
source exposes a supported definition; the harness does not claim an upstream
lineage edge unless it is asserted separately.

The harness never prints or persists the BI key or OpenMetadata JWT outside
temporary files, which are removed on exit. The quickstart admin password is
`admin` by default; set `OPENMETADATA_ADMIN_EMAIL` and
`OPENMETADATA_ADMIN_PASSWORD` for a different test account.
