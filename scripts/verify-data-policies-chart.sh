#!/usr/bin/env bash
# Chart test for data policies (ADR 0028, #1145): with trino.dataPolicies on,
# the coordinator runs OPA as a loopback-only sidecar pinned by digest, gated on
# its bundles, with the Rego and the OPA properties installed from the plugin
# release; OPA is Trino's third access control; the API and worker run with
# BALEIA_DATA_POLICIES=opa. Rendering with `helm template` needs no cluster.
set -euo pipefail

chart="${1:-deploy/helm/baleia}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
openssl genrsa -out "$work/key.pem" 2048 2>/dev/null
openssl rsa -in "$work/key.pem" -pubout -out "$work/key.pub" 2>/dev/null

render() {
  helm template baleia "$chart" \
    --set secrets.create=true \
    --set secrets.values.JWT_SECRET=abcdef0123456789abcdef \
    --set secrets.values.BALEIA_ENCRYPTION_KEY=fedcba9876543210fedcba9876543210 \
    --set secrets.values.DATABASE_URL=postgresql://b \
    --set secrets.values.BALEIA_TRINO_PASSWORD=trino-password \
    --set trino.enabled=true \
    --set trino.plugin.version=0.11.0 \
    "$@"
}

manifest="$(render \
  --set trino.dataPolicies.enabled=true \
  --set-file trino.dataPolicies.publicKey="$work/key.pub" \
  --set-file secrets.values.BALEIA_DATA_POLICY_SIGNING_KEY="$work/key.pem")"

has() {
  printf '%s\n' "$manifest" | grep -F -- "$1" >/dev/null || fail "$2"
}

has 'access-control.config-files=/etc/trino/baleia-base-access-control.properties,/etc/trino/baleia-catalog-guard.properties,/etc/trino/opa/opa-access-control.properties' \
  "OPA is not the third access control after the base rules and the guard"
has '- --addr=127.0.0.1:8181' "OPA's decision API is not bound to loopback"
has 'path: "/health?bundles=true"' "OPA's readiness does not wait for its bundles"
has 'resource: file:///policy/policy.tar.gz' "OPA does not load the Rego from the policy volume"
has 'persist: true' "the data bundle is not persisted"
has 'keyid: baleia' "the data bundle is not verified"
has 'cp "$SRC/baleia-opa-policy.tar.gz" /policy/policy.tar.gz' "install-plugin does not install the Rego"
has 'mountPath: /etc/trino/opa' "Trino does not mount the OPA properties"
has 'BALEIA_DATA_POLICIES: "opa"' "the API and worker do not get BALEIA_DATA_POLICIES=opa"
has 'BALEIA_OPA_TOKEN:' "the Secret has no OPA token"
has 'BALEIA_DEFAULT_ENGINE_ID:' "the Secret has no default engine id"
has '"trusted_identities":["baleia_admin","baleia"]' "OPA's trusted identities are not the guard's admin and service users"

printf '%s\n' "$manifest" | grep -E 'image: docker.io/openpolicyagent/opa:[^ ]+@sha256:[0-9a-f]{64}$' >/dev/null \
  || fail "the OPA image is not pinned by digest"
[[ "$(printf '%s\n' "$manifest" | grep -c 'key: BALEIA_DATA_POLICIES$')" -eq 2 ]] \
  || fail "the API and the worker must both reference BALEIA_DATA_POLICIES"
# The chart job installs kubeconform before this runs; a workstation without it
# skips the schema check only.
if command -v kubeconform >/dev/null; then
  printf '%s\n' "$manifest" | kubeconform -strict -summary -ignore-missing-schemas >/dev/null \
    || fail "the manifests with data policies do not validate"
else
  echo "skip: kubeconform is not installed" >&2
fi

# Off: no sidecar, no OPA properties, no policy volume.
off="$(render)"
if printf '%s\n' "$off" | grep -E 'opa-access-control|name: opa$|BALEIA_DATA_POLICIES:' >/dev/null; then
  fail "data policies off still renders OPA"
fi

# On without the public key or the signing key: refused.
if render --set trino.dataPolicies.enabled=true \
  --set-file secrets.values.BALEIA_DATA_POLICY_SIGNING_KEY="$work/key.pem" >/dev/null 2>&1; then
  fail "data policies render without trino.dataPolicies.publicKey"
fi
if render --set trino.dataPolicies.enabled=true \
  --set-file trino.dataPolicies.publicKey="$work/key.pub" >/dev/null 2>&1; then
  fail "data policies render without BALEIA_DATA_POLICY_SIGNING_KEY"
fi

echo "ok: data policies render OPA as a gated, loopback-only sidecar of the coordinator"
