# OpenMetadata through the Baleia BI gateway

This harness validates the supported integration path from issue #1122:
OpenMetadata's stock Trino connector connects to a workspace-scoped Baleia BI
gateway. It does not add an OpenMetadata connector to Baleia and it does not
emit OpenLineage events.

The harness pins OpenMetadata `1.12.6` and the official quickstart compose file
by SHA-256. The ingestion image and server use the same pinned release, as
required by OpenMetadata. The release can be overridden for a deliberate
version-matrix run, together with its compose checksum.

## Run

The Baleia minikube environment must be running and seeded first. The normal
setup is:

```sh
make e2e
# Optional, manual or scheduled integration validation:
make e2e-openmetadata
make e2e-down
```

`make e2e` leaves the cluster running after its checks. The seed writes the
workspace BI key to `/tmp/baleia-e2e-bi-key.secret`; set `BI_E2E_KEY_FILE` when
using another path.

`make e2e-openmetadata` is intentionally not called by `make e2e` or the
ordinary PR CI path. The OpenMetadata quickstart is a heavyweight external
integration dependency, so run this target manually or from a dedicated
nightly/scheduled integration job.

The script starts and removes a temporary OpenMetadata quickstart, forwards
the Baleia BI gateway, logs in with the quickstart's default test admin, runs
the stock `metadata ingest` command, asserts the service and both tables through
the OpenMetadata API, and removes all OpenMetadata containers and volumes on
exit. The local gateway hop is HTTP only inside the test; a deployed
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
gateway with the workspace BI key and ingest physical metadata through the
Trino protocol. The gateway still enforces the workspace catalog scope and
read-only policy on every request.

The preceding `make e2e` run also covers the negative gateway contract: an
out-of-scope catalog is denied and a disposable BI key is revoked and rejected
on its next request. Those checks stay in the protocol harness because they do
not require starting OpenMetadata.

The current Baleia gateway records connector introspection as `origin=bi`, so
those statements are visible in query history. This is intentional and is the
behavior that #1122 documents; changing history classification belongs in a
follow-up implementation decision, not in this test harness.

Views are enabled in the OpenMetadata workflow. The seeded TPCH catalog does
not provide a mutable view fixture, so view-lineage support must be validated
against a Trino catalog that exposes view definitions before #1123 claims it
as supported.

The harness never prints or persists the BI key or OpenMetadata JWT outside
temporary files, which are removed on exit. The quickstart admin password is
`admin` by default; set `OPENMETADATA_ADMIN_EMAIL` and
`OPENMETADATA_ADMIN_PASSWORD` for a different test account.
