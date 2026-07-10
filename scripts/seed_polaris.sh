#!/usr/bin/env bash
# Seed the local dev Polaris with one catalog, namespace and table so the
# Catalog Explorer endpoints (and their Bruno tests) have something to list.
#
# Storage is Ceph RGW (S3-compatible, no STS). The catalog uses
# stsUnavailable=true + static S3 credentials in its properties so Polaris
# writes metadata straight to Ceph instead of falling back to real AWS.
# (The global SKIP_CREDENTIAL_SUBSCOPING_INDIRECTION flag must NOT be set —
# it blanks the storage config and sends requests to s3.amazonaws.com.)
#
# Requires the docker-compose stack up (polaris + ceph). Safe to re-run.
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

# S3 endpoint Polaris uses server-side (container network) and vends to clients.
S3_ENDPOINT="${S3_ENDPOINT:-http://ceph:8080}"
S3_ACCESS_KEY="${S3_ACCESS_KEY:-seaweedfs}"
S3_SECRET_KEY="${S3_SECRET_KEY:-seaweedfs}"
S3_REGION="${S3_REGION:-us-east-1}"
BUCKET="${BUCKET:-baleia}"
BASE_LOCATION="s3://${BUCKET}/warehouse"

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

echo "==> Creating catalog '${CATALOG}' (S3 -> Ceph, stsUnavailable + static creds)"
curl -s -o /dev/null -w "    catalog: HTTP %{http_code}\n" -X POST "${POLARIS_URL}/api/management/v1/catalogs" "${auth[@]}" -d "{
  \"catalog\": {
    \"name\": \"${CATALOG}\", \"type\": \"INTERNAL\",
    \"properties\": {
      \"default-base-location\": \"${BASE_LOCATION}\",
      \"s3.access-key-id\": \"${S3_ACCESS_KEY}\",
      \"s3.secret-access-key\": \"${S3_SECRET_KEY}\",
      \"s3.endpoint\": \"${S3_ENDPOINT}\",
      \"s3.path-style-access\": \"true\",
      \"s3.region\": \"${S3_REGION}\"
    },
    \"storageConfigInfo\": {
      \"storageType\": \"S3\",
      \"allowedLocations\": [\"${BASE_LOCATION}\"],
      \"endpoint\": \"${S3_ENDPOINT}\",
      \"endpointInternal\": \"${S3_ENDPOINT}\",
      \"pathStyleAccess\": true,
      \"stsUnavailable\": true
    }
  }
}"

echo "==> Creating namespace '${NAMESPACE}'"
curl -s -o /dev/null -w "    namespace: HTTP %{http_code}\n" -X POST "${POLARIS_URL}/api/catalog/v1/${CATALOG}/namespaces" "${auth[@]}" \
  -d "{\"namespace\":[\"${NAMESPACE}\"],\"properties\":{}}"

echo "==> Creating table '${TABLE}' (writes metadata.json to Ceph)"
curl -s -o /dev/null -w "    table: HTTP %{http_code}\n" -X POST "${POLARIS_URL}/api/catalog/v1/${CATALOG}/namespaces/${NAMESPACE}/tables" "${auth[@]}" \
  -d "{\"name\":\"${TABLE}\",\"schema\":{\"type\":\"struct\",\"fields\":[{\"id\":1,\"name\":\"id\",\"type\":\"long\",\"required\":true},{\"id\":2,\"name\":\"event_name\",\"type\":\"string\",\"required\":false},{\"id\":3,\"name\":\"created_at\",\"type\":\"timestamptz\",\"required\":false}]}}"

echo "==> Done: ${CATALOG}.${NAMESPACE}.${TABLE}"
