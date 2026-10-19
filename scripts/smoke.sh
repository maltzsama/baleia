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
READY_URL="${API%/api/v1}/readyz"
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
  if curl -sf "$READY_URL" | grep -q ok; then break; fi
  sleep 2
done
curl -sf "$READY_URL" | grep -q ok || { echo "FAIL api not ready"; exit 1; }

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

# ── 3. Seed the Trino catalog and a workspace target ────────────────────────
# The registry row makes `tpch_test` exist in Trino; the target seed exposes it
# to the smoke admin's personal workspace (catalog + native binding + grant),
# which POST /executions requires.
step "seed tpch_test"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ "$STACK" == "k8s" ]]; then
  kubectl -n baleia-dev exec -i statefulset/postgres -- psql -U baleia -d baleia < "$ROOT/db/seed/tpch_test.sql" >/dev/null
  kubectl -n baleia-dev exec -i statefulset/postgres -- psql -U baleia -d baleia -v email="$EMAIL" < "$ROOT/db/seed/smoke_target.sql" >/dev/null
  # Trino loads catalogs from the registry at boot; restart it so it picks up
  # the row that was just seeded.
  kubectl -n baleia-dev rollout restart deploy/baleia-trino >/dev/null
  kubectl -n baleia-dev rollout status deploy/baleia-trino --timeout=300s >/dev/null
else
  (cd "$ROOT" && make seed-tpch >/dev/null)
  docker compose -f "$ROOT/docker-compose.yaml" exec -T postgres \
    psql -U baleia -d baleia -v email="$EMAIL" < "$ROOT/db/seed/smoke_target.sql" >/dev/null
fi

# ── 4. Personal workspace ───────────────────────────────────────────────────
step "resolve workspace"
WS=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/auth/me" | jq -r '.personal_workspace_id')
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