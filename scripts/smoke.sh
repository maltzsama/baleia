#!/usr/bin/env bash
# End-to-end smoke: setup admin, seed tpch, run a query through the API and
# verify the value through Trino, on the given STACK (compose or k8s).
#
# Usage:
#   STACK=compose ./scripts/smoke.sh     # after `docker compose up -d` + `make seed-tpch`, api reachable
#   STACK=k8s     ./scripts/smoke.sh     # after `make k8s-up`, with `make k8s-forward`
#
# Requires: jq, curl. Exits non-zero on the first failure.

set -euo pipefail

STACK="${STACK:-compose}"
API="${API:-http://localhost:8080/api/v1}"
TRINO_SQL="SELECT count(*) FROM tpch_test.tiny.nation"
EXPECTED=25

step() { echo; echo "── $*"; }

# trino_sql runs a statement inside the cluster's Trino and returns stdout.
trino_sql() {
  if [[ "$STACK" == "k8s" ]]; then
    kubectl -n baleia-dev exec -i deploy/baleia-trino -- trino --output-format TSV --execute "$1"
  else
    docker compose -f docker-compose.yaml exec -T trino trino --output-format TSV --execute "$1"
  fi
}

# ── 1. API ready ─────────────────────────────────────────────────────────────
step "wait for API /readyz"
for _ in {1..60}; do
  if curl -sf "$API/../readyz" | grep -q ok; then break; fi
  sleep 2
done
curl -sf "$API/../readyz" | grep -q ok || { echo "FAIL api not ready"; exit 1; }

# ── 2. First admin (or reuse) ───────────────────────────────────────────────
step "create/login admin"
EMAIL="smoke@baleia.dev"
PASSWORD="smoke-admin-pass-12345"
resp=$(curl -sf -X POST "$API/setup/admin" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"smoke\",\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" 2>/dev/null || true)
if [[ -z "${resp:-}" ]]; then
  resp=$(curl -sf -X POST "$API/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")
fi
TOKEN=$(jq -r '.token' <<<"$resp")
[[ -n "$TOKEN" && "$TOKEN" != "null" ]] || { echo "FAIL no token"; exit 1; }

# ── 3. Seed tpch registry row ───────────────────────────────────────────────
step "seed tpch_test"
if [[ "$STACK" == "k8s" ]]; then
  kubectl -n baleia-dev exec -i deploy/postgres -- psql -U baleia -d baleia < db/seed/tpch_test.sql >/dev/null
  # Trino loads catalogs from the registry at boot; make it pick it up.
  trino_sql "SELECT 1" >/dev/null || true
else
  (cd "$(dirname "$0")/.." && make seed-tpch >/dev/null)
fi

# ── 4. Personal workspace ───────────────────────────────────────────────────
step "resolve workspace"
WS=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/workspaces" | jq -r '.[0].id')
[[ -n "$WS" && "$WS" != "null" ]] || { echo "FAIL no workspace"; exit 1; }

# ── 5. Run a query through the API ──────────────────────────────────────────
step "run SELECT count(*) via API"
EXEC=$(curl -sf -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"sql\":\"$TRINO_SQL\"}" "$API/workspaces/$WS/executions")
EID=$(jq -r '.id' <<<"$EXEC")
[[ -n "$EID" && "$EID" != "null" ]] || { echo "FAIL no execution"; exit 1; }

step "poll execution $EID"
STATUS=running
for _ in {1..60}; do
  STATUS=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/workspaces/$WS/executions/$EID" | jq -r '.status')
  [[ "$STATUS" == "running" ]] || break
  sleep 2
done
[[ "$STATUS" == "finished" ]] || { echo "FAIL execution ended $STATUS"; exit 1; }

# ── 6. Value check through Trino ────────────────────────────────────────────
step "verify value on the cluster"
VALUE=$(trino_sql "$TRINO_SQL" | tr -d '[:space:]')
[[ "$VALUE" == "$EXPECTED" ]] || { echo "FAIL TPCH count=$VALUE, want $EXPECTED"; exit 1; }

echo
echo "SMOKE PASS ($STACK): admin ok, workspace ok, query finished, tpch.tiny.nation = $EXPECTED."