#!/usr/bin/env bash
# Data policies fail closed (ADR 0028, #1145). The coordinator's OPA sidecar is
# frozen (`docker pause` on the minikube node: OPA is its PID namespace's init,
# which ignores SIGSTOP sent from inside the pod): every statement then fails,
# the member's and the trusted admin's alike, and none returns rows. Once OPA
# resumes, the member gets the EU rows again. It needs the forwards of
# `make e2e-forward`, the seed of `make e2e-seed` and the Docker runtime that
# `make k8s-up` starts minikube with.
set -euo pipefail

ns="${K8S_NS:-baleia-dev}"
profile="${K8S_PROFILE:-baleia}"
trino="${E2E_TRINO_URL:-https://localhost:8081}"
api="${E2E_API_URL:-http://localhost:8080}"
ca="${E2E_TRINO_CA:-${TMPDIR:-/tmp}/baleia-e2e-trino-ca.pem}"
password="${E2E_PASSWORD:-e2e-password}"
here="$(cd "$(dirname "$0")" && pwd)"
table="e2e_governed.sales.customers"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

work="$(mktemp -d)"
container=""
# unpause resumes the frozen OPA container. It also succeeds when the
# container no longer needs it: the kubelet may have replaced it after its
# liveness probe failed, which leaves the old one stopped, not paused.
unpause() {
  minikube -p "$profile" ssh -- docker unpause "$container" >/dev/null 2>&1 && return 0
  [ "$(minikube -p "$profile" ssh -- docker inspect -f '{{.State.Paused}}' "$container" 2>/dev/null | tr -d '\r')" != "true" ]
}
resume() {
  if [ -n "$container" ]; then
    unpause || true
  fi
  rm -rf "$work"
}
trap resume EXIT

kubectl -n "$ns" get secret baleia-secret -o jsonpath='{.data.BALEIA_TRINO_PASSWORD}' | base64 -d >"$work/password"
login="$(python3 -c 'import json,sys; print(json.dumps({"email": "member@e2e.test", "password": sys.argv[1]}))' "$password")"
# Logins are limited to 10 per minute (#391) and the Playwright suite just ran:
# on 429, wait for Retry-After and sign in again.
token=""
for _ in $(seq 1 10); do
  status="$(curl -sS -o "$work/login.json" -D "$work/login.headers" -w '%{http_code}' -X POST \
    "$api/api/v1/auth/login" -H 'Content-Type: application/json' -d "$login")"
  if [ "$status" = "200" ]; then
    token="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$work/login.json")"
    break
  fi
  [ "$status" = "429" ] || fail "login as the member = $status: $(cat "$work/login.json")"
  wait_s="$(tr -d '\r' <"$work/login.headers" | awk -F': ' 'tolower($1)=="retry-after" {print $2}')"
  sleep "$(( ${wait_s:-6} + 1 ))"
done
[ -n "$token" ] || fail "could not sign in as the member"
member_id="$(curl -fsS "$api/api/v1/auth/me" -H "Authorization: Bearer $token" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
member="u_$member_id"

query() { # user sql -> JSON result
  python3 "$here/trino_query.py" "$trino" "$ca" "$work/password" "$1" "$2"
}
field() { # json key
  python3 -c 'import json,sys; v=json.loads(sys.argv[1])[sys.argv[2]]; print(json.dumps(v) if isinstance(v, list) else (v or ""))' "$1" "$2"
}
member_regions() {
  query "$member" "SELECT DISTINCT region FROM $table" | python3 -c 'import json,sys; r=json.load(sys.stdin); print(",".join(sorted(x[0] for x in r["rows"])) if r["state"] == "FINISHED" else "ERROR " + str(r["error"]))'
}

[ "$(member_regions)" = "EU" ] || fail "before the pause the member does not get only the EU rows: $(member_regions)"
echo "ok    the member gets the EU rows while OPA is up"

pod="$(kubectl -n "$ns" get pods -l app.kubernetes.io/component=trino -o jsonpath='{.items[0].metadata.name}')"
id="$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.status.containerStatuses[?(@.name=="opa")].containerID}')"
[[ "$id" == docker://* ]] || fail "the OPA container id '$id' is not a Docker one"
minikube -p "$profile" ssh -- docker pause "${id#docker://}" >/dev/null
container="${id#docker://}"
echo "ok    OPA frozen in $pod"

# Both statements wait on OPA until Trino's OPA client gives up or the
# kubelet restarts the frozen container; either way they must fail.
query "$member" "SELECT * FROM $table" >"$work/member.json" &
member_pid=$!
query "baleia_admin" "SELECT * FROM $table" >"$work/admin.json" &
admin_pid=$!
wait "$member_pid" "$admin_pid"
for who in member admin; do
  result="$(cat "$work/$who.json")"
  state="$(field "$result" state)"
  rows="$(field "$result" rows)"
  [ "$state" = "FAILED" ] || fail "with OPA down the $who's statement ended $state with rows $rows"
  [ "$rows" = "[]" ] || fail "with OPA down the $who got rows: $rows"
  # Trino itself must refuse the statement for want of OPA; a dropped
  # connection (TRANSPORT_ERROR above) or another failure proves nothing.
  error="$(field "$result" error)"
  [[ "$error" == *"OPA"* ]] || fail "with OPA down the $who's statement failed for another reason: $error"
  echo "ok    with OPA down the $who's statement fails: $(field "$result" error | head -c 160)"
done

# Forget the container only once it runs again, so the EXIT handler retries.
unpause || fail "could not unpause OPA in $pod"
container=""
kubectl -n "$ns" wait --for=condition=ready pod -l app.kubernetes.io/component=trino --timeout=5m >/dev/null
for _ in $(seq 1 30); do
  [ "$(member_regions)" = "EU" ] && break
  sleep 2
done
[ "$(member_regions)" = "EU" ] || fail "after OPA resumed the member does not get the EU rows: $(member_regions)"
echo "ok    the policies hold again once OPA resumes"
echo "DATA POLICIES OPA-DOWN E2E PASS"
