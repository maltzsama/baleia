#!/usr/bin/env bash
# Seed the local dev Polaris with one catalog, namespace and table so the
# Catalog Explorer endpoints (and their Bruno tests) have something to list.
#
# Uses FILE storage (local filesystem inside the Polaris container), which
# requires the ALLOW_INSECURE_STORAGE_TYPES feature flag — set in
# docker-compose.yaml. This avoids the S3/SeaweedFS credential path entirely,
# which is all we need for the read-only explorer endpoints.
#
# Requires the docker-compose stack to be up (polaris). Safe to re-run:
# the catalog is dropped and recreated from scratch.
#
#   ./scripts/seed_polaris.sh
set -euo pipefail

POLARIS_URL="${POLARIS_URL:-http://localhost:8181}"
POLARIS_REALM="${POLARIS_REALM:-POLARIS}"
POLARIS_CLIENT_ID="${POLARIS_CLIENT_ID:-admin}"
POLARIS_CLIENT_SECRET="${POLARIS_CLIENT_SECRET:-admin}"

CATALOG="${CATALOG:-baleia}"
NAMESPACE="${NAMESPACE:-analytics}"
TABLE="${TABLE:-events}"
BASE_LOCATION="file:///tmp/warehouse/${CATALOG}"

echo "==> Getting Polaris OAuth token"
TOKEN=$(curl -s -X POST "${POLARIS_URL}/api/catalog/v1/oauth/tokens" \
  -H "Polaris-Realm: ${POLARIS_REALM}" \
  -d "grant_type=client_credentials" \
  -d "client_id=${POLARIS_CLIENT_ID}" \
  -d "client_secret=${POLARIS_CLIENT_SECRET}" \
  -d "scope=PRINCIPAL_ROLE:ALL" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])")

auth=(-H "Authorization: Bearer ${TOKEN}" -H "Polaris-Realm: ${POLARIS_REALM}" -H "Content-Type: application/json")

echo "==> Dropping existing catalog '${CATALOG}' (if any)"
curl -s -o /dev/null -X DELETE "${auth[@]}" "${POLARIS_URL}/api/catalog/v1/${CATALOG}/namespaces/${NAMESPACE}/tables/${TABLE}" || true
curl -s -o /dev/null -X DELETE "${auth[@]}" "${POLARIS_URL}/api/catalog/v1/${CATALOG}/namespaces/${NAMESPACE}" || true
curl -s -o /dev/null -X DELETE "${auth[@]}" "${POLARIS_URL}/api/management/v1/catalogs/${CATALOG}" || true

echo "==> Creating catalog '${CATALOG}' (FILE storage)"
curl -s -o /dev/null -w "    catalog: HTTP %{http_code}\n" -X POST "${POLARIS_URL}/api/management/v1/catalogs" "${auth[@]}" \
  -d "{\"catalog\":{\"name\":\"${CATALOG}\",\"type\":\"INTERNAL\",\"properties\":{\"default-base-location\":\"${BASE_LOCATION}\"},\"storageConfigInfo\":{\"storageType\":\"FILE\",\"allowedLocations\":[\"${BASE_LOCATION}\"]}}}"

echo "==> Creating namespace '${NAMESPACE}'"
curl -s -o /dev/null -w "    namespace: HTTP %{http_code}\n" -X POST "${POLARIS_URL}/api/catalog/v1/${CATALOG}/namespaces" "${auth[@]}" \
  -d "{\"namespace\":[\"${NAMESPACE}\"],\"properties\":{}}"

echo "==> Creating table '${TABLE}'"
curl -s -o /dev/null -w "    table: HTTP %{http_code}\n" -X POST "${POLARIS_URL}/api/catalog/v1/${CATALOG}/namespaces/${NAMESPACE}/tables" "${auth[@]}" \
  -d "{\"name\":\"${TABLE}\",\"schema\":{\"type\":\"struct\",\"fields\":[{\"id\":1,\"name\":\"id\",\"type\":\"long\",\"required\":true},{\"id\":2,\"name\":\"event_name\",\"type\":\"string\",\"required\":false},{\"id\":3,\"name\":\"created_at\",\"type\":\"timestamptz\",\"required\":false}]}}"

echo "==> Done: ${CATALOG}.${NAMESPACE}.${TABLE}"
