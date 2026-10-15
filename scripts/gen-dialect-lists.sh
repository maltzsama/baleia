#!/usr/bin/env bash
# Regenerates internal/dialect/lists/<dialect>.{keywords,functions}.txt from a
# real server on the minikube dev cluster (make k8s-up), then runs that
# dialect's execution test (TestExecute_<dialect>) against the same server.
#
#   scripts/gen-dialect-lists.sh <dialect> [--keep]
#
# postgresql and trino use the dev stack's services; mysql, mariadb,
# sqlserver, oracle and clickhouse start deploy/dev/sources/<name>.yaml and
# delete it afterwards unless --keep is given (the images are large). sqlite
# runs in process; flightsql-generic has no server and only gets headers.
set -euo pipefail

dialect="${1:?usage: $0 <dialect> [--keep]}"
keep="${2:-}"
ns="${K8S_NS:-baleia-dev}"
cd "$(dirname "$0")/.."

pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  if [[ -n "${source_file:-}" && "$keep" != "--keep" ]]; then
    # Delete the source and its image from the node: they are large.
    local image
    image=$(grep -m1 'image:' "$source_file" | awk '{print $2}')
    kubectl delete -f "$source_file" --wait=true >/dev/null
    kubectl -n "$ns" wait --for=delete pod -l "app=src-$source_name" --timeout=5m >/dev/null 2>&1 || true
    minikube -p "${K8S_PROFILE:-baleia}" image rm "$image" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# forward <service> <local port> <remote port>
forward() {
  kubectl -n "$ns" port-forward "svc/$1" "$2:$3" >/dev/null 2>&1 &
  pids+=($!)
  for _ in $(seq 1 30); do
    (echo >"/dev/tcp/127.0.0.1/$2") 2>/dev/null && return 0
    sleep 1
  done
  echo "port-forward to $1 did not come up" >&2
  return 1
}

# source <name>: start a source database and wait until it answers.
start_source() {
  source_name=$1
  source_file="deploy/dev/sources/$1.yaml"
  kubectl apply -f "$source_file" >/dev/null
  kubectl -n "$ns" rollout status "deployment/src-$1" --timeout=15m
}

# wait_ready <cmd...>: retry a readiness probe inside the source pod.
wait_ready() {
  local name=$1; shift
  for _ in $(seq 1 120); do
    kubectl -n "$ns" exec "deployment/src-$name" -- "$@" >/dev/null 2>&1 && return 0
    sleep 5
  done
  echo "$name did not become ready" >&2
  return 1
}

run_tests() {
  BALEIA_UPDATE_LISTS="$dialect" go test -count=1 -run TestGenerateLists -v ./internal/dialect/ | grep -E 'words|FAIL|ok'
  # The execution test must run, not skip.
  go test -count=1 -run "TestExecute_${exec_name:-$dialect}\$" -v ./internal/dialect/ | tee /dev/stderr | grep -q -- "--- PASS: TestExecute_"
}

case "$dialect" in
  postgresql)
    forward postgres 15432 5432
    export BALEIA_TEST_POSTGRES_URL="postgres://baleia:baleia@127.0.0.1:15432/baleia?sslmode=disable"
    run_tests ;;
  trino)
    forward baleia-trino 18088 8080
    export BALEIA_TEST_TRINO_URL="http://127.0.0.1:18088"
    run_tests ;;
  mysql)
    start_source mysql
    wait_ready mysql mysqladmin ping -h 127.0.0.1 -pbaleia_dev
    forward src-mysql 13306 3306
    export BALEIA_TEST_MYSQL_URL="mysql://root:baleia_dev@127.0.0.1:13306"
    run_tests ;;
  mariadb)
    start_source mariadb
    wait_ready mariadb healthcheck.sh --connect --innodb_initialized
    forward src-mariadb 13307 3306
    export BALEIA_TEST_MARIADB_URL="mysql://root:baleia_dev@127.0.0.1:13307"
    run_tests ;;
  tsql)
    start_source sqlserver
    wait_ready sqlserver /opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P Baleia_dev_2026 -Q "SELECT 1"
    forward src-sqlserver 11433 1433
    export BALEIA_TEST_SQLSERVER_URL="sqlserver://sa:Baleia_dev_2026@127.0.0.1:11433"
    run_tests ;;
  oracle)
    start_source oracle
    wait_ready oracle healthcheck.sh
    forward src-oracle 11521 1521
    # Lists read V$ views (SYSTEM); the execution test uses the app user.
    BALEIA_TEST_ORACLE_URL="oracle://system:baleia_dev@127.0.0.1:11521/FREEPDB1" \
      BALEIA_UPDATE_LISTS=oracle go test -count=1 -run TestGenerateLists -v ./internal/dialect/ | grep -E 'words|FAIL|ok'
    BALEIA_TEST_ORACLE_URL="oracle://baleia:baleia_dev@127.0.0.1:11521/FREEPDB1" \
      go test -count=1 -run 'TestExecute_oracle$' -v ./internal/dialect/ | tee /dev/stderr | grep -q -- "--- PASS: TestExecute_" ;;
  clickhouse)
    start_source clickhouse
    wait_ready clickhouse wget -qO- http://127.0.0.1:8123/ping
    forward src-clickhouse 18123 8123
    export BALEIA_TEST_CLICKHOUSE_URL="http://baleia:baleia_dev@127.0.0.1:18123"
    run_tests ;;
  sqlite|flightsql-generic)
    BALEIA_UPDATE_LISTS="$dialect" go test -count=1 -run TestGenerateLists -v ./internal/dialect/ | grep -E 'words|FAIL|ok' ;;
  *)
    echo "unknown dialect: $dialect" >&2
    exit 2 ;;
esac
