#!/usr/bin/env bash
# Chart test for #1101: the BI gateway listener, its dedicated Service/Ingress,
# the rendered environment and the secret wiring must agree, and an unusable
# public URL must fail at install time, not at a BI tool's first request.
set -euo pipefail

chart="${1:-deploy/helm/baleia}"
release=baleia

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

render() {
  helm template "$release" "$chart" --set existingSecret=x "$@"
}

# grep reads the whole stream (no -q): with pipefail, an early exit would turn
# the producer's SIGPIPE into a failure.
expect() {
  local manifest="$1" pattern="$2" label="$3"
  printf '%s\n' "$manifest" | grep "$pattern" >/dev/null || fail "$label"
}

valid="$(render \
  --set biGateway.enabled=true \
  --set biGateway.publicURL=https://bi.example.com \
  --set biGateway.ingress.enabled=true \
  --set biGateway.ingress.className=nginx \
  --set 'biGateway.ingress.hosts[0]=bi.example.com')" || fail "the valid gateway configuration did not render"

# 1) The listener address and public URL are rendered into the API config and
#    referenced by the API Deployment.
expect "$valid" 'BALEIA_BI_GATEWAY_ADDR: ":8443"' "BALEIA_BI_GATEWAY_ADDR is not :8443"
expect "$valid" 'BALEIA_BI_GATEWAY_URL: "https://bi.example.com"' "BALEIA_BI_GATEWAY_URL is not the public URL"
expect "$valid" 'containerPort: 8443' "the API Deployment has no bi-gateway containerPort"
expect "$valid" 'name: BALEIA_BI_GATEWAY_SECRET' "the API Deployment does not reference the gateway secret"

# 2) The dedicated Service and Ingress target the listener at the server root.
expect "$valid" 'name: baleia-bi-gateway' "no dedicated BI gateway Service/Ingress"
expect "$valid" 'name: trino' "the gateway Service has no trino port"
expect "$valid" 'path: /' "the gateway Ingress does not serve the server root"

# 3) The in-cluster fallback is used when publicURL is empty.
fallback="$(render --set biGateway.enabled=true)" || fail "the empty-publicURL configuration did not render"
expect "$fallback" 'BALEIA_BI_GATEWAY_URL: "http://baleia-bi-gateway\.' "the in-cluster URL fallback is missing"

# 4) Unusable public URLs must fail the render (exit non-zero).
expect_fail() {
  local label="$1"
  shift
  if render "$@" >/dev/null 2>&1; then
    fail "$label was accepted; it must fail the render"
  fi
}
expect_fail "a public URL with a path" --set biGateway.publicURL=https://bi.example.com/path
expect_fail "a public URL with credentials" --set biGateway.publicURL=https://user:pass@bi.example.com
expect_fail "a non-http public URL" --set biGateway.publicURL=bi.example.com
expect_fail "a cleartext public URL with the Ingress" \
  --set biGateway.ingress.enabled=true \
  --set 'biGateway.ingress.hosts[0]=bi.example.com' \
  --set biGateway.publicURL=http://bi.example.com

echo "ok: the BI gateway chart renders consistently and rejects unusable public URLs"
