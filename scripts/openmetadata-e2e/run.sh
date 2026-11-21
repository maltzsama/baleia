#!/usr/bin/env bash
set -euo pipefail

# OpenMetadata's Trino ingestion is intentionally exercised outside Baleia's
# process. This script starts the pinned upstream quickstart, runs the stock
# ingestion image, and points it at the already seeded Baleia BI gateway.

OPENMETADATA_VERSION="${OPENMETADATA_VERSION:-1.12.6}"
OPENMETADATA_COMPOSE_SHA256="${OPENMETADATA_COMPOSE_SHA256:-d19f9c1877368621452b626ae24e01722010d6fdcb0dc43b2b36bfc871f13f38}"
OPENMETADATA_COMPOSE_URL="${OPENMETADATA_COMPOSE_URL:-https://raw.githubusercontent.com/open-metadata/OpenMetadata/${OPENMETADATA_VERSION}-release/docker/docker-compose-quickstart/docker-compose.yml}"
OPENMETADATA_PROJECT="${OPENMETADATA_PROJECT:-baleia-om-e2e}"
OPENMETADATA_SERVICE="${OPENMETADATA_SERVICE:-baleia-bi-gateway}"
OPENMETADATA_CATALOG="${OPENMETADATA_CATALOG:-e2e_tpch}"
OPENMETADATA_SCHEMA="${OPENMETADATA_SCHEMA:-tiny}"
OPENMETADATA_USER="${OPENMETADATA_USER:-admin@e2e.test}"
OPENMETADATA_ADMIN_EMAIL="${OPENMETADATA_ADMIN_EMAIL:-admin@open-metadata.org}"
OPENMETADATA_ADMIN_PASSWORD="${OPENMETADATA_ADMIN_PASSWORD:-admin}"
OPENMETADATA_SERVER_PORT="${OPENMETADATA_SERVER_PORT:-18585}"
OPENMETADATA_ADMIN_PORT="${OPENMETADATA_ADMIN_PORT:-18586}"
OPENMETADATA_GATEWAY_PORT="${OPENMETADATA_GATEWAY_PORT:-18443}"
OPENMETADATA_GATEWAY_URL="${OPENMETADATA_GATEWAY_URL:-https://127.0.0.1:8444}"
OPENMETADATA_K8S_NAMESPACE="${OPENMETADATA_K8S_NAMESPACE:-baleia-dev}"
OPENMETADATA_KEY_FILE="${OPENMETADATA_KEY_FILE:-${BI_E2E_KEY_FILE:-/tmp/baleia-e2e-bi-key.secret}}"

if [[ ! -r "$OPENMETADATA_KEY_FILE" ]]; then
  echo "openmetadata-e2e: BI key file is not readable: $OPENMETADATA_KEY_FILE" >&2
  echo "Run the e2e seed first or set OPENMETADATA_KEY_FILE." >&2
  exit 1
fi

for command in curl docker go jq kubectl sha256sum; do
  command -v "$command" >/dev/null || {
    echo "openmetadata-e2e: required command is missing: $command" >&2
    exit 1
  }
done

workdir="$(mktemp -d "${TMPDIR:-/tmp}/baleia-openmetadata-e2e.XXXXXX")"
compose="$workdir/docker-compose.yml"
compose_override="$workdir/docker-compose.override.yml"
workflow="$workdir/trino-workflow.yml"
gateway_log="$workdir/gateway-port-forward.log"
tls_proxy_log="$workdir/gateway-tls-proxy.log"
tls_proxy="$workdir/gateway-tls-proxy"
gateway_pid=""
tls_proxy_pid=""
compose_args=(-p "$OPENMETADATA_PROJECT" -f "$compose" -f "$compose_override")

cleanup() {
  if [[ -n "$gateway_pid" ]]; then
    kill "$gateway_pid" 2>/dev/null || true
    wait "$gateway_pid" 2>/dev/null || true
  fi
  if [[ -n "$tls_proxy_pid" ]]; then
    kill "$tls_proxy_pid" 2>/dev/null || true
    wait "$tls_proxy_pid" 2>/dev/null || true
  fi
  docker compose "${compose_args[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  # The quickstart bind-mounts MySQL data and creates root-owned files in the
  # temporary directory. Remove those files from a short-lived container so a
  # failed or interrupted run does not leave credentials or database data in
  # /tmp.
  if [[ -d "$workdir/docker-volume" ]]; then
    docker run --rm -v "$workdir/docker-volume:/cleanup" alpine:3.20 \
      find /cleanup -mindepth 1 -delete >/dev/null 2>&1 || true
  fi
  find "$workdir" -mindepth 1 -delete >/dev/null 2>&1 || true
  rmdir "$workdir" >/dev/null 2>&1 || true
}
trap cleanup EXIT

curl -fsSL -o "$compose" "$OPENMETADATA_COMPOSE_URL"
actual_sha256="$(sha256sum "$compose" | cut -d ' ' -f1)"
if [[ "$actual_sha256" != "$OPENMETADATA_COMPOSE_SHA256" ]]; then
  echo "openmetadata-e2e: compose checksum mismatch for OpenMetadata $OPENMETADATA_VERSION" >&2
  echo "got $actual_sha256, want $OPENMETADATA_COMPOSE_SHA256" >&2
  exit 1
fi

# Keep the quickstart's containers isolated from another local OpenMetadata
# instance that may already own the default ports.
cat >"$compose_override" <<EOF
services:
  mysql:
    container_name: !reset null
    ports: !override []
  elasticsearch:
    container_name: !reset null
    ports: !override []
  execute-migrate-all:
    container_name: !reset null
  openmetadata-server:
    container_name: !reset null
    ports: !override
      - "127.0.0.1:$OPENMETADATA_SERVER_PORT:8585"
      - "127.0.0.1:$OPENMETADATA_ADMIN_PORT:8586"
  ingestion:
    container_name: !reset null
    network_mode: host
    networks: !reset []
    ports: !override []
networks:
  app_net:
    ipam: !reset {}
EOF

# The gateway port-forward is plain HTTP behind the local TLS proxy. Production
# deployments must put TLS in front of the BI listener because the BI key is a
# password credential. The proxy also preserves the public URL that the
# already-deployed gateway signs into Trino nextUri values.
gateway_basic_auth="$OPENMETADATA_USER:$(<"$OPENMETADATA_KEY_FILE")"
if [[ "$OPENMETADATA_GATEWAY_URL" == "https://127.0.0.1:8444" ]]; then
  gateway_probe_url="$OPENMETADATA_GATEWAY_URL"
  kubectl -n "$OPENMETADATA_K8S_NAMESPACE" port-forward "svc/$OPENMETADATA_SERVICE" \
    "${OPENMETADATA_GATEWAY_PORT}:8443" >"$gateway_log" 2>&1 &
  gateway_pid=$!
  for _ in $(seq 1 60); do
    if curl -kfsS -u "$gateway_basic_auth" -o /dev/null --max-time 1 "http://127.0.0.1:${OPENMETADATA_GATEWAY_PORT}/v1/info"; then
      break
    fi
    sleep 1
  done
  curl -kfsS -u "$gateway_basic_auth" -o /dev/null --max-time 2 "http://127.0.0.1:${OPENMETADATA_GATEWAY_PORT}/v1/info" || {
    echo "openmetadata-e2e: BI gateway did not become reachable" >&2
    sed -n '1,80p' "$gateway_log" >&2 || true
    exit 1
  }
  go build -o "$tls_proxy" ./scripts/bi-e2e
  BI_E2E_TLS_ADDR=127.0.0.1:8444 BI_E2E_TLS_TARGET="127.0.0.1:${OPENMETADATA_GATEWAY_PORT}" \
    "$tls_proxy" >"$tls_proxy_log" 2>&1 &
  tls_proxy_pid=$!
  for _ in $(seq 1 30); do
    if curl -kfsS -u "$gateway_basic_auth" -o /dev/null --max-time 1 "$gateway_probe_url/v1/info"; then
      break
    fi
    sleep 1
  done
  curl -kfsS -u "$gateway_basic_auth" -o /dev/null --max-time 2 "$gateway_probe_url/v1/info" || {
    echo "openmetadata-e2e: BI TLS proxy did not become reachable" >&2
    sed -n '1,80p' "$tls_proxy_log" >&2 || true
    exit 1
  }
fi

gateway_scheme=http
gateway_hostport="$OPENMETADATA_GATEWAY_URL"
if [[ "$gateway_hostport" == https://* ]]; then
  gateway_scheme=https
  gateway_hostport="${gateway_hostport#https://}"
else
  gateway_hostport="${gateway_hostport#http://}"
fi

docker compose "${compose_args[@]}" up -d

for _ in $(seq 1 120); do
  if curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:${OPENMETADATA_ADMIN_PORT}/healthcheck"; then
    break
  fi
  sleep 2
done
curl -fsS -o /dev/null --max-time 5 "http://127.0.0.1:${OPENMETADATA_ADMIN_PORT}/healthcheck" || {
  echo "openmetadata-e2e: OpenMetadata did not become healthy" >&2
  docker compose "${compose_args[@]}" logs --tail=120 openmetadata-server >&2 || true
  exit 1
}

admin_password_base64="$(printf %s "$OPENMETADATA_ADMIN_PASSWORD" | base64 -w0)"
login_response="$workdir/openmetadata-login.json"
curl -fsS --retry 5 --retry-delay 2 \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$OPENMETADATA_ADMIN_EMAIL\",\"password\":\"$admin_password_base64\"}" \
  -o "$login_response" \
  "http://127.0.0.1:${OPENMETADATA_SERVER_PORT}/api/v1/users/login"
openmetadata_token="$(jq -er '.accessToken' "$login_response")"

# The token is passed only through the temporary workflow and is removed by
# cleanup. It is never printed or persisted in the repository.
cat >"$workflow" <<EOF
source:
  type: trino
  serviceName: $OPENMETADATA_SERVICE
  serviceConnection:
    config:
      type: Trino
      hostPort: $gateway_hostport
      username: $OPENMETADATA_USER
      authType:
        password: $(<"$OPENMETADATA_KEY_FILE")
      catalog: $OPENMETADATA_CATALOG
      databaseSchema: $OPENMETADATA_SCHEMA
      connectionOptions: {}
      connectionArguments:
        http_scheme: $gateway_scheme
        legacy_prepared_statements: false
        verify: false
  sourceConfig:
    config:
      type: DatabaseMetadata
      includeTables: true
      includeViews: true
      markDeletedTables: false
      markDeletedStoredProcedures: false
      tableFilterPattern:
        includes:
          - "^orders$"
          - "^customer$"
sink:
  type: metadata-rest
  config: {}
workflowConfig:
  loggerLevel: INFO
  openMetadataServerConfig:
    hostPort: http://127.0.0.1:${OPENMETADATA_SERVER_PORT}/api
    authProvider: openmetadata
    securityConfig:
      jwtToken: $openmetadata_token
EOF

# The image's entrypoint is Airflow's bootstrap shell. Override it for the
# one-shot stock ingestion command. The compose override uses the host network
# so this local test can reach both host-published endpoints deterministically.
docker compose "${compose_args[@]}" run --rm --no-deps \
  --entrypoint metadata \
  -v "$workflow:/tmp/baleia-trino-workflow.yml:ro" \
  ingestion ingest -c /tmp/baleia-trino-workflow.yml

api_base="http://127.0.0.1:${OPENMETADATA_SERVER_PORT}/api/v1"
curl -fsS -H "Authorization: Bearer $openmetadata_token" \
  "$api_base/services/databaseServices/name/$OPENMETADATA_SERVICE" >/dev/null
for table in orders customer; do
  curl -fsS -H "Authorization: Bearer $openmetadata_token" \
    "$api_base/tables/name/$OPENMETADATA_SERVICE.$OPENMETADATA_CATALOG.$OPENMETADATA_SCHEMA.$table" >/dev/null
done

echo "openmetadata-e2e: OpenMetadata $OPENMETADATA_VERSION ingested $OPENMETADATA_CATALOG.$OPENMETADATA_SCHEMA through the Baleia BI gateway"
echo "openmetadata-e2e: metadata scans are recorded by Baleia as origin=bi; inspect query history separately when validating noise policy"
