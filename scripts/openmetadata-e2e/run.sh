#!/usr/bin/env bash
set -euo pipefail

# OpenMetadata's Trino ingestion is intentionally exercised outside Baleia's
# process. This script starts the pinned upstream quickstart, runs the stock
# ingestion image, and points it at the already seeded Baleia BI gateway.

# Supported OpenMetadata releases, each pinned by the SHA-256 of its quickstart
# compose file. `make e2e-openmetadata` runs every release in
# OPENMETADATA_VERSIONS; set OPENMETADATA_COMPOSE_SHA256 to try another one.
OPENMETADATA_VERSION="${OPENMETADATA_VERSION:-2.1.0}"
case "$OPENMETADATA_VERSION" in
  2.0.9) pinned_compose_sha256=54dbae6828e1dddb9fd2fcccc868121d353d518ded64b47e75828d43ca64034f ;;
  2.1.0) pinned_compose_sha256=34b145e3a682ef10f43513f4436b4373b99889a96caf6480277f27889d0bf9c9 ;;
  *) pinned_compose_sha256="" ;;
esac
OPENMETADATA_COMPOSE_SHA256="${OPENMETADATA_COMPOSE_SHA256:-$pinned_compose_sha256}"
if [[ -z "$OPENMETADATA_COMPOSE_SHA256" ]]; then
  echo "openmetadata-e2e: OpenMetadata $OPENMETADATA_VERSION is not pinned; set OPENMETADATA_COMPOSE_SHA256" >&2
  exit 1
fi
OPENMETADATA_COMPOSE_URL="${OPENMETADATA_COMPOSE_URL:-https://raw.githubusercontent.com/open-metadata/OpenMetadata/${OPENMETADATA_VERSION}-release/docker/docker-compose-quickstart/docker-compose.yml}"
OPENMETADATA_PROJECT="${OPENMETADATA_PROJECT:-baleia-om-e2e-${OPENMETADATA_VERSION//./-}}"
OPENMETADATA_SERVICE="${OPENMETADATA_SERVICE:-baleia-bi-gateway}"
OPENMETADATA_CATALOG="${OPENMETADATA_CATALOG:-e2e_tpch}"
OPENMETADATA_SCHEMA="${OPENMETADATA_SCHEMA:-tiny}"
OPENMETADATA_VIEW_NAME="${OPENMETADATA_VIEW_NAME:-}"
OPENMETADATA_USER="${OPENMETADATA_USER:-admin@e2e.test}"
OPENMETADATA_ADMIN_EMAIL="${OPENMETADATA_ADMIN_EMAIL:-admin@open-metadata.org}"
OPENMETADATA_ADMIN_PASSWORD="${OPENMETADATA_ADMIN_PASSWORD:-admin}"
OPENMETADATA_SERVER_PORT="${OPENMETADATA_SERVER_PORT:-18585}"
OPENMETADATA_ADMIN_PORT="${OPENMETADATA_ADMIN_PORT:-18586}"
OPENMETADATA_GATEWAY_PORT="${OPENMETADATA_GATEWAY_PORT:-18443}"
OPENMETADATA_GATEWAY_URL="${OPENMETADATA_GATEWAY_URL:-https://127.0.0.1:8444}"
OPENMETADATA_K8S_NAMESPACE="${OPENMETADATA_K8S_NAMESPACE:-baleia-dev}"
OPENMETADATA_KEY_FILE="${OPENMETADATA_KEY_FILE:-${BI_E2E_KEY_FILE:-/tmp/baleia-e2e-bi-key.secret}}"

view_filter_yaml=""
if [[ -n "$OPENMETADATA_VIEW_NAME" ]]; then
  # OpenMetadata treats tableFilterPattern as a regular expression. Quote the
  # configured name as a literal and then encode the complete YAML scalar so
  # names containing regex or YAML punctuation remain unambiguous.
  view_regex="$(printf '%s' "$OPENMETADATA_VIEW_NAME" | sed 's/[][\\.^$|(){}*+?]/\\&/g')"
  view_filter_yaml="$(printf '^%s$' "$view_regex" | jq -Rr @json)"
fi

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
$(if [[ -n "$view_filter_yaml" ]]; then printf '          - %s\n' "$view_filter_yaml"; fi)
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
  table_response="$workdir/$table.json"
  curl -fsS -H "Authorization: Bearer $openmetadata_token" \
    -o "$table_response" \
    "$api_base/tables/name/$OPENMETADATA_SERVICE.$OPENMETADATA_CATALOG.$OPENMETADATA_SCHEMA.$table"
  jq -e '(.columns | length) > 0' "$table_response" >/dev/null || {
    echo "openmetadata-e2e: $table was ingested without columns" >&2
    exit 1
  }
done

if [[ -n "$OPENMETADATA_VIEW_NAME" ]]; then
  view_response="$workdir/$OPENMETADATA_VIEW_NAME.json"
  view_fqn="$OPENMETADATA_SERVICE.$OPENMETADATA_CATALOG.$OPENMETADATA_SCHEMA.$OPENMETADATA_VIEW_NAME"
  view_fqn_encoded="$(printf '%s' "$view_fqn" | jq -sRr @uri)"
  curl -fsS -H "Authorization: Bearer $openmetadata_token" \
    -o "$view_response" \
    "$api_base/tables/name/$view_fqn_encoded?fields=schemaDefinition"
  jq -e '(.tableType | ascii_downcase) == "view" and (.schemaDefinition // "") != ""' \
    "$view_response" >/dev/null || {
    echo "openmetadata-e2e: $OPENMETADATA_VIEW_NAME was not ingested as a view with a definition" >&2
    exit 1
  }
fi

# The semantic catalog routes (ADR 0030) are what the baleia-openmetadata
# connector reads. Check them with the same key, and check that every physical
# table they name is a table OpenMetadata just ingested through the Trino
# connector: that name match is what lets the connector draw lineage.
# The seed creates the semantic fixtures on e2e_tpch.tiny only, so the
# semantic checks run when the harness ingests that schema and are skipped
# for a custom view fixture.
semantic_checked=false
if [[ "$OPENMETADATA_CATALOG.$OPENMETADATA_SCHEMA" == "e2e_tpch.tiny" ]]; then
  semantic_get() {
    curl -kfsS -u "$gateway_basic_auth" "$OPENMETADATA_GATEWAY_URL/baleia/v1/semantic$1"
  }
  semantic_summary="$workdir/semantic.json"
  semantic_get "" >"$semantic_summary"
  jq -e '.contract_version == 1 and .models >= 1 and .metrics >= 1' "$semantic_summary" >/dev/null || {
    echo "openmetadata-e2e: the semantic catalog summary is not what the seed created" >&2
    jq . "$semantic_summary" >&2 || true
    exit 1
  }
  semantic_models="$workdir/semantic-models.json"
  semantic_metrics="$workdir/semantic-metrics.json"
  semantic_get "/models?limit=500" >"$semantic_models"
  semantic_get "/metrics?limit=500" >"$semantic_metrics"
  model_orders="$OPENMETADATA_CATALOG.$OPENMETADATA_SCHEMA.orders"
  jq -e --arg t "$model_orders" '
    any(.items[]; .slug == "e2e_bi_orders"
      and .source.table == $t
      and .source_tables == [$t]
      and any(.columns[]; .name == "order_status" and .type == "string")
      and any(.metrics[]; .name == "order_count" and (.sql | length) > 0))' "$semantic_models" >/dev/null || {
    echo "openmetadata-e2e: the seeded model e2e_bi_orders is missing or incomplete in the semantic catalog" >&2
    jq '.items[] | select(.slug == "e2e_bi_orders")' "$semantic_models" >&2 || true
    exit 1
  }
  jq -e --arg t "$model_orders" '
    any(.items[]; .slug == "e2e_bi_order_count" and .source_tables == [$t] and (.sql | length) > 0)' \
    "$semantic_metrics" >/dev/null || {
    echo "openmetadata-e2e: the seeded metric e2e_bi_order_count is missing or incomplete in the semantic catalog" >&2
    exit 1
  }
  jq -e '[.items[].slug] | index("e2e_orders") == null' "$semantic_models" >/dev/null || {
    echo "openmetadata-e2e: the semantic catalog exposed another workspace's model" >&2
    exit 1
  }
  jq -r '.items[].source_tables[]' "$semantic_models" "$semantic_metrics" | sort -u | while read -r source_table; do
    fqn_encoded="$(printf '%s' "$OPENMETADATA_SERVICE.$source_table" | jq -sRr @uri)"
    curl -fsS -o /dev/null -H "Authorization: Bearer $openmetadata_token" "$api_base/tables/name/$fqn_encoded" || {
      echo "openmetadata-e2e: semantic source table $source_table is not an OpenMetadata table of $OPENMETADATA_SERVICE" >&2
      exit 1
    }
  done
  semantic_checked=true
else
  echo "openmetadata-e2e: semantic catalog checks skipped: they need the seeded e2e_tpch.tiny schema"
fi

echo "openmetadata-e2e: OpenMetadata $OPENMETADATA_VERSION ingested $OPENMETADATA_CATALOG.$OPENMETADATA_SCHEMA through the Baleia BI gateway"
echo "openmetadata-e2e: physical table columns were present in OpenMetadata"
if [[ -n "$OPENMETADATA_VIEW_NAME" ]]; then
  echo "openmetadata-e2e: view $OPENMETADATA_VIEW_NAME and its definition were present in OpenMetadata"
fi
if [[ "$semantic_checked" == true ]]; then
  echo "openmetadata-e2e: the semantic catalog names its source tables as OpenMetadata ingested them"
fi
echo "openmetadata-e2e: metadata scans are recorded by Baleia as origin=bi; inspect query history separately when validating noise policy"
