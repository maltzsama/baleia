#!/usr/bin/env bash
# Chart test for #992: the bundled Trino's security configuration must live in
# an immutable ConfigMap whose name carries a hash of its content, and the
# coordinator must mount that exact ConfigMap. Rendering with `helm template`
# needs no cluster, so this runs in the chart job on every pull request.
set -euo pipefail

chart="${1:-deploy/helm/baleia}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

manifest="$(helm template baleia "$chart" \
  --set secrets.create=true \
  --set secrets.values.JWT_SECRET=abcdef0123456789abcdef \
  --set secrets.values.BALEIA_ENCRYPTION_KEY=fedcba9876543210fedcba9876543210 \
  --set secrets.values.DATABASE_URL=postgresql://b \
  --set secrets.values.BALEIA_TRINO_PASSWORD=trino-password \
  --set trino.enabled=true \
  --set trino.plugin.version=0.1.0)"

# The ConfigMap that carries the catalog guard, config.properties and the rest
# of the Trino security configuration: hashed name + immutable.
cm_name="$(printf '%s\n' "$manifest" | awk '
  function record() {
    if (!found && name ~ /-trino-config-[0-9a-f]+$/) {
      found = 1
      result = name
      result_immutable = immutable
    }
    name = ""
    immutable = 0
  }
  /^---$/ { record(); next }
  /^  name: .+$/ { name = $2 }
  /^immutable: true$/ { immutable = 1 }
  END {
    record()
    if (!found) exit 3
    if (!result_immutable) exit 4
    print result
  }
')" || fail "the Trino ConfigMap is missing or not immutable"

[[ "$cm_name" =~ -trino-config-[0-9a-f]{10}$ ]] \
  || fail "ConfigMap name '$cm_name' does not carry a content hash"

digest="${cm_name##*-}"

# The coordinator mounts the hashed ConfigMap and rolls with the same hash.
printf '%s\n' "$manifest" | grep -A2 -- '- name: trino-config' | grep "name: ${cm_name}" >/dev/null \
  || fail "the coordinator does not mount ${cm_name}"
printf '%s\n' "$manifest" | grep "checksum/trino-config: ${digest}" >/dev/null \
  || fail "the coordinator is missing checksum/trino-config: ${digest}"

echo "ok: ${cm_name} is immutable and mounted by the coordinator"
