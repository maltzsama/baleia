# OKE demo deploy

Hardened overlay for the public demo: the `deploy/dev` dependencies minus
everything that only exists for local development (`rustfs`,
`rustfs-bucket-init`, `polaris-console`, the Temporal UI), with real secrets
from a Kubernetes Secret instead of the development defaults.

Target: Oracle OKE (Basic, Always Free control plane) in `sa-saopaulo-1`,
worker nodes in the worker subnet, Object Storage for the lake and the query
results, ingress-nginx on the flexible LB with cert-manager.

## Order

1. **Secret** (never committed):

   ```bash
   BALEIA_DEMO_ENV=~/.baleia-demo.env deploy/oke/create-secrets.sh
   ```

   Keys: `JWT_SECRET`, `BALEIA_ENCRYPTION_KEY`, `DATABASE_URL`,
   `BALEIA_TRINO_DB_PASSWORD`, `PG_PASSWORD`, `POLARIS_SECRET`,
   `POLARIS_BOOTSTRAP_CREDENTIALS`, `POLARIS_BASIC_USERS`, `S3_ACCESS_KEY`,
   `S3_SECRET_KEY`.

2. **Dependencies** (Postgres, Temporal, Polaris and the Polaris seed):

   ```bash
   kubectl apply -k deploy/oke
   ```

   `postgres.baleia-dev.svc` is hard-coded by the chart's Trino init container,
   which is why everything runs in `baleia-dev`.

3. **Baleia chart**:

   ```bash
   helm upgrade --install baleia oci://ghcr.io/maltzsama/charts/baleia \
     --version 0.5.0 -n baleia-dev -f deploy/oke/values-oke.yaml
   ```

   Replace `<object-storage-namespace>` in `values-oke.yaml` first
   (`oci os ns get`).

4. **Ingress + TLS** (ingress-nginx and cert-manager must already be running):

   ```bash
   kubectl apply -f deploy/oke/ingress.yaml
   ```

## Chart caveats on OKE 1.36

CRI-O runs with `short_name_mode = enforcing`, so images without a registry
host fail with `ImageInspectError`. `values-oke.yaml` qualifies the Trino
image, but the chart still hard-codes two:

```bash
kubectl -n baleia-dev patch deployment baleia-trino --type=json -p '[
  {"op":"replace","path":"/spec/template/spec/initContainers/0/image","value":"docker.io/library/alpine:3.20"},
  {"op":"replace","path":"/spec/template/spec/initContainers/1/image","value":"docker.io/library/postgres:18"}]'
```

Re-run it after every `helm upgrade` until the chart templates use fully
qualified image names.

## Cluster prerequisites

- OKE Basic cluster with flannel and the node subnet configured as
  `serviceLbSubnetIds`.
- Block Volume CSI controller. The OKE-managed node driver is installed, but
  the controller Deployment and the `blockvolume.csi.oraclecloud.com`
  CSIDriver object are not on this cluster. Install the controller from
  `oracle/oci-cloud-controller-manager` (drop its control-plane
  `nodeSelector`) and a `oci-volume-provisioner` Secret with a user principal
  (API key) auth block, or enable the add-on when creating the cluster.
- ingress-nginx with the OCI flexible-LB annotations, cert-manager with CRDs.
