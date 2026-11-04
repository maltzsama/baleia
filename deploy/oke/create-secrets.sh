#!/usr/bin/env bash
# Creates the demo-secrets Secret in the demo namespace from a local env file.
#
#   BALEIA_DEMO_ENV=~/.baleia-demo.env deploy/oke/create-secrets.sh
#
# The env file must define: JWT_SECRET, BALEIA_ENCRYPTION_KEY, PG_PASSWORD,
# TRINO_DB_PASSWORD, POLARIS_SECRET, S3_ACCESS_KEY, S3_SECRET_KEY. Never
# commit it.
set -euo pipefail

: "${BALEIA_DEMO_ENV:=$HOME/.baleia-demo.env}"
: "${NS:=baleia-dev}"

# shellcheck disable=SC1090
set -a; . "$BALEIA_DEMO_ENV"; set +a

for key in JWT_SECRET BALEIA_ENCRYPTION_KEY PG_PASSWORD TRINO_DB_PASSWORD \
           POLARIS_SECRET S3_ACCESS_KEY S3_SECRET_KEY; do
  [[ -n "${!key:-}" ]] || { echo "missing $key in $BALEIA_DEMO_ENV" >&2; exit 1; }
done

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

kubectl -n "$NS" create secret generic demo-secrets \
  --from-literal=JWT_SECRET="$JWT_SECRET" \
  --from-literal=BALEIA_ENCRYPTION_KEY="$BALEIA_ENCRYPTION_KEY" \
  --from-literal=DATABASE_URL="postgresql://baleia:${PG_PASSWORD}@postgres.${NS}.svc:5432/baleia?sslmode=disable" \
  --from-literal=BALEIA_TRINO_DB_PASSWORD="$TRINO_DB_PASSWORD" \
  --from-literal=PG_PASSWORD="$PG_PASSWORD" \
  --from-literal=POLARIS_SECRET="$POLARIS_SECRET" \
  --from-literal=POLARIS_BOOTSTRAP_CREDENTIALS="POLARIS,admin,${POLARIS_SECRET}" \
  --from-literal=POLARIS_BASIC_USERS="admin:${POLARIS_SECRET}" \
  --from-literal=S3_ACCESS_KEY="$S3_ACCESS_KEY" \
  --from-literal=S3_SECRET_KEY="$S3_SECRET_KEY" \
  --from-literal=SMTP_PASSWORD="${SMTP_PASSWORD:-}" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "demo-secrets applied in namespace $NS"
