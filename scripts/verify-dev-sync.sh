#!/usr/bin/env bash
# Keeps the tracked copies under deploy/dev/files/ in sync with their
# originals (kustomize refuses to read files outside the kustomization dir).
set -euo pipefail

pairs=(
  "scripts/seed_polaris.py deploy/dev/files/seed_polaris.py"
  "deploy/trino/config.properties deploy/helm/baleia/files/trino/config.properties"
  "deploy/trino/catalog-store.properties deploy/helm/baleia/files/trino/catalog-store.properties"
  "deploy/trino/baleia-catalog-guard.properties deploy/helm/baleia/files/trino/baleia-catalog-guard.properties"
)

fail=0
for pair in "${pairs[@]}"; do
  read -r src dst <<<"$pair"
  if ! diff -q "$src" "$dst" >/dev/null; then
    echo "DRIFT: $src and $dst differ. Re-run: cp $src $dst" >&2
    fail=1
  fi
done
[[ $fail -eq 0 ]] && echo "deploy/dev files in sync"