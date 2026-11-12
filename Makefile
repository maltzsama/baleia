BALEIA_DIR ?= $(CURDIR)

# The baleia-trino-catalog-store release Baleia runs and tests its contract
# against (#88, #39): the single pin for compose, the dev cluster and CI.
BALEIA_PLUGIN_VERSION ?= 0.10.2
TRINO_VERSION         ?= 484
BALEIA_PLUGIN_TARBALL  = baleia-catalog-store-$(BALEIA_PLUGIN_VERSION)+trino$(TRINO_VERSION).tar.gz
BALEIA_PLUGIN_URL     ?= https://github.com/maltzsama/baleia-trino-catalog-store/releases/download/v$(BALEIA_PLUGIN_VERSION)/$(BALEIA_PLUGIN_TARBALL)

# Fallback secret for local development. An exported or command-line
# JWT_SECRET wins, so credentials encrypted under a custom key stay readable.
JWT_SECRET ?= dev-secret-not-for-production

# Credentials are sealed with BALEIA_ENCRYPTION_KEY, never JWT_SECRET. Same
# override rule: an exported or command-line value wins.
BALEIA_ENCRYPTION_KEY ?= dev-encryption-key-not-for-production-0123

.PHONY: dev worker migrate-credentials migrate-oidc-providers store-migrate store-migrate-down store-migrate-status generate lint lint-all vuln deadcode buildbuild-web build-all seed-tpch contract-fetch up down down-v logs restart-trino k8s-up k8s-plugin-bundle k8s-forward k8s-ldap-forward k8s-logs k8s-seed-tpch k8s-down k8s-source k8s-source-down e2e e2e-forward e2e-bi e2e-down

dev:
	JWT_SECRET=$(JWT_SECRET) BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) BALEIA_AGENTS_ENABLED=true SMTP_HOST=localhost SMTP_PORT=1025 SMTP_FROM=noreply@baleia.io PORT=9000 go run ./cmd/api

worker:
	JWT_SECRET=$(JWT_SECRET) BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) BALEIA_AGENTS_ENABLED=true go run ./cmd/worker

# ── Trino plugin ──────────────────────────────────────────────────────────────
# The plugin is never built here: compose and the dev cluster install the
# released tarball (sha256-verified) into the official Trino image.

# contract-fetch extracts contract/ of the pinned release into
# internal/contract/testdata for the contract tests (go test -tags contract).
.PHONY: contract-fetch
contract-fetch:
	@rm -rf internal/contract/testdata && mkdir -p internal/contract/testdata
	curl -fsSL -o internal/contract/testdata/p.tgz "$(BALEIA_PLUGIN_URL)"
	curl -fsSL -o internal/contract/testdata/p.sha256 "$(BALEIA_PLUGIN_URL).sha256"
	cd internal/contract/testdata && echo "$$(cut -d' ' -f1 p.sha256)  p.tgz" | sha256sum -c - \
		&& tar -xzf p.tgz contract VERSION \
		&& test "$$(cat VERSION)" = "$(BALEIA_PLUGIN_VERSION)+trino$(TRINO_VERSION)" \
		&& rm p.tgz p.sha256
	@echo ">>> contract of plugin $$(cat internal/contract/testdata/VERSION) in internal/contract/testdata"

# ── Compose ───────────────────────────────────────────────────────────────────

# up: brings up the full stack; trino-plugin-init installs the plugin release.
up:
	BALEIA_PLUGIN_VERSION=$(BALEIA_PLUGIN_VERSION) TRINO_VERSION=$(TRINO_VERSION) docker compose up -d

# down: tears down the stack without destroying volumes (data persists).
down:
	docker compose down

# down-v: tears down the stack and removes named volumes + stopped containers +
# orphaned volumes. WARNING: destroys postgres, temporal, etc. data.
down-v:
	docker compose rm -fsv
	docker compose down -v
	docker container prune -f
	docker volume prune -f

logs:
	docker compose logs -f api worker

restart-trino:
	docker compose restart trino

# ── Store migrations (GORM, ADR 0020) ─────────────────────────────────────────
# The store schema is versioned Go migrations applied by the GORM runner; there
# is no generated migration or query tooling left to run.

store-migrate:
	go run ./cmd/migrate -cmd=store-up

store-migrate-down:
	go run ./cmd/migrate -cmd=store-down

store-migrate-status:
	go run ./cmd/migrate -cmd=store-status

# One-shot: re-seal legacy connection credentials (encrypted under JWT_SECRET)
# as v1 envelopes under BALEIA_ENCRYPTION_KEY. Idempotent. Needs DATABASE_URL.
migrate-credentials:
	JWT_SECRET=$(JWT_SECRET) BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) go run ./cmd/migrate -cmd=credentials

# One-shot: seal legacy plaintext auth_providers.config_enc values (and re-seal
# previous-key envelopes) under BALEIA_ENCRYPTION_KEY. Idempotent.
migrate-oidc-providers:
	BALEIA_ENCRYPTION_KEY=$(BALEIA_ENCRYPTION_KEY) go run ./cmd/migrate -cmd=oidc-providers

# ── Seed ──────────────────────────────────────────────────────────────────────

# Inserts the tpch_test test row into trino_catalog_registry, used by the
# T1-T9 acceptance tests of the baleia-trino-catalog-store plugin. Idempotent.
seed-tpch:
	docker compose exec -T postgres psql -U baleia -d baleia < db/seed/tpch_test.sql

# ── End-to-end (#106) ─────────────────────────────────────────────────────────
# The dev Trino's CA (#647), for the seed's HTTPS login.
E2E_TRINO_CA ?= $(or $(TMPDIR),/tmp)/baleia-e2e-trino-ca.pem
# The BI gateway JDBC e2e (ADR 0017): the reference Trino-protocol client.
BI_E2E_JDBC_VERSION ?= 484
BI_E2E_JDBC_JAR ?= $(or $(TMPDIR),/tmp)/trino-jdbc-$(BI_E2E_JDBC_VERSION).jar
BI_E2E_KEY_FILE ?= $(or $(TMPDIR),/tmp)/baleia-e2e-bi-key.secret
BI_E2E_URL ?= https://localhost:8444
BI_E2E_TLS_PROXY ?= $(or $(TMPDIR),/tmp)/baleia-bi-e2e-tlsproxy
# Runs against the minikube dev cluster (not compose). Port-forwards the API,
# Postgres (for the seed) and Trino (for the run smoke).
e2e:
	$(MAKE) k8s-up
	$(MAKE) e2e-forward
	@echo "waiting for the API on :8080…"
	@until curl -fsS http://localhost:8080/readyz >/dev/null 2>&1; do sleep 1; done
	@kubectl -n $(K8S_NS) wait --for=condition=ready pod -l app=postgres --timeout=120s
	@sleep 2
	@kubectl -n $(K8S_NS) get secret baleia-trino-tls -o jsonpath='{.data.ca\.crt}' | base64 -d > $(E2E_TRINO_CA)
	@grep -q 'BEGIN CERTIFICATE' $(E2E_TRINO_CA) || { echo "e2e: could not read ca.crt from secret baleia-trino-tls" >&2; exit 1; }
	@pw="$$(kubectl -n $(K8S_NS) get secret baleia-secret -o jsonpath='{.data.BALEIA_TRINO_PASSWORD}' | base64 -d)"; \
	if [ -z "$$pw" ]; then echo "e2e: could not read BALEIA_TRINO_PASSWORD from secret baleia-secret" >&2; exit 1; fi; \
	DATABASE_URL="postgresql://baleia:baleia@localhost:5433/baleia?sslmode=disable" \
		E2E_TRINO_URL=https://localhost:8081 \
		E2E_TRINO_CA_FILE=$(E2E_TRINO_CA) \
		E2E_TRINO_PASSWORD="$$pw" \
		E2E_BI_KEY_FILE=$(BI_E2E_KEY_FILE) \
		go run -tags e2e ./cmd/e2e-seed
	$(MAKE) e2e-bi
	cd web && E2E_BASE_URL=http://localhost:8080 npx playwright test

e2e-forward:
	kubectl -n $(K8S_NS) port-forward svc/baleia-api 8080:8080 &
	kubectl -n $(K8S_NS) port-forward svc/postgres 5433:5432 &
	kubectl -n $(K8S_NS) port-forward svc/baleia-trino 8081:8443 &
	kubectl -n $(K8S_NS) port-forward svc/baleia-bi-gateway 8443:8443 &

# Drives the Trino protocol through the gateway with the Trino JDBC driver:
# Basic auth, a query, multi-page follows, every replica validating a nextUri,
# cancellation, a scope-denied catalog, and inline pages despite the spooling
# header. cmd/e2e-seed (called by e2e) wrote the BI key secret.
e2e-bi:
	@command -v java >/dev/null || { echo "e2e-bi: java (17+) is required" >&2; exit 1; }
	@test -f $(BI_E2E_JDBC_JAR) || curl -fsSL -o $(BI_E2E_JDBC_JAR) \
		https://repo1.maven.org/maven2/io/trino/trino-jdbc/$(BI_E2E_JDBC_VERSION)/trino-jdbc-$(BI_E2E_JDBC_VERSION).jar
	@go build -o $(BI_E2E_TLS_PROXY) ./scripts/bi-e2e
	@BI_E2E_TLS_ADDR=127.0.0.1:8444 BI_E2E_TLS_TARGET=127.0.0.1:8443 \
		$(BI_E2E_TLS_PROXY) > /tmp/baleia-bi-e2e-tls.log 2>&1 & \
	proxy=$$!; \
	pids=""; \
	trap 'kill $$proxy $$pids 2>/dev/null' EXIT; \
	api_pods="$$(kubectl -n $(K8S_NS) get pods -l app.kubernetes.io/component=api -o jsonpath='{.items[*].metadata.name}')"; \
	pods=""; port=8540; \
	for pod in $$api_pods; do \
		port=$$((port+1)); \
		kubectl -n $(K8S_NS) port-forward pod/$$pod $$port:8443 >/dev/null 2>&1 & \
		pids="$$pids $$!"; \
		for _ in $$(seq 1 50); do curl -s --max-time 2 -o /dev/null http://127.0.0.1:$$port/v1/info && break; sleep 0.2; done; \
		curl -sS -o /dev/null http://127.0.0.1:$$port/v1/info || { echo "e2e-bi: pod $$pod is not reachable on port $$port" >&2; exit 1; }; \
		pods="$$pods,http://127.0.0.1:$$port"; \
	done; \
	for _ in $$(seq 1 50); do curl -s --max-time 2 -k -o /dev/null $(BI_E2E_URL)/v1/info && break; sleep 0.2; done; \
	curl -sSk -o /dev/null $(BI_E2E_URL)/v1/info || { echo "e2e-bi: the gateway is not reachable at $(BI_E2E_URL)" >&2; exit 1; }; \
	BI_E2E_URL=$(BI_E2E_URL) BI_E2E_USER=admin@e2e.test \
		BI_E2E_KEY_FILE=$(BI_E2E_KEY_FILE) BI_E2E_CATALOG=e2e_tpch BI_E2E_SCHEMA=tiny \
		BI_E2E_PODS="$${pods#,}" \
		java -cp $(BI_E2E_JDBC_JAR) scripts/bi-e2e/BiGatewayE2E.java

e2e-down:
	K8S_PURGE=1 $(MAKE) k8s-down

# ── Code Generation ───────────────────────────────────────────────────────────

# Pin the generators so a different local binary cannot rewrite the checked-in
# code and break the build (#208).
OAPI_CODEGEN_VERSION ?= v2.4.1

generate:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) --config=config/oapi-models.yaml api/swagger.yaml
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) --config=config/oapi-server.yaml api/swagger.yaml

# ── Build ─────────────────────────────────────────────────────────────────────

# Lint and vulnerability scan (#369). The pins match CI. `lint` reports only
# what is new since the merge base with origin/main (existing findings are a
# baseline); `lint-all` reports everything.
GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION ?= v1.9.0
DEADCODE_VERSION ?= v0.38.0
GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# The module's Go packages. Not ./...: that walks every directory, including a
# local tmp/ (e.g. a root-owned Postgres data dir) it cannot read (#637).
GO_PACKAGES ?= ./cmd/... ./internal/... ./tests/...

lint:
	$(GOLANGCI_LINT) run --new-from-merge-base=origin/main $(GO_PACKAGES)

lint-all:
	$(GOLANGCI_LINT) run $(GO_PACKAGES)

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $(GO_PACKAGES)

# Functions nothing reaches: not a binary, a test, nor a build-tagged suite.
# golangci-lint's unused skips exported names and counts any use from a test of
# the same package; this catches a helper whose last caller was removed.
# deadcode exits 0 on findings, so any output fails the target.
DEADCODE_TAGS ?= contract e2e integration
deadcode:
	@out="$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION) -test -tags '$(DEADCODE_TAGS)' $(GO_PACKAGES))" || exit 1; \
	if [ -n "$$out" ]; then echo "$$out"; echo "unreachable functions: remove them or call them" >&2; exit 1; fi

build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

build-web:
	cd web && npm run build

build-all: build-web build

# ── Kubernetes (minikube) ─────────────────────────────────────────────────────

K8S_PROFILE ?= baleia
K8S_NS      ?= baleia-dev
# CI overrides these to fit the runner; the default fits a workstation.
K8S_CPUS    ?= 6
K8S_MEMORY  ?= 12g
K8S_DRIVER  ?=
K8S_DRIVER_FLAG = $(if $(K8S_DRIVER),--driver=$(K8S_DRIVER),)
K8S_PLUGIN_IMAGE      ?= baleia-plugins
K8S_PLUGIN_VERSION    ?= $(BALEIA_PLUGIN_VERSION)
# The bundle image is tagged with the plugin release it carries (the plugin repo
# owns that version), so it matches trino.plugin.image in deploy/dev/values-dev.yaml.
K8S_PLUGIN_TAG        ?= $(K8S_PLUGIN_VERSION)

# Bundle the RELEASED catalog-store plugin (deploy/dev/plugins.Dockerfile
# downloads the GitHub-release tarball and verifies its sha256) into a sidecar
# image for the Trino initContainer. Builds on the host docker and loads into
# minikube (the node's overlay is small and GitHub may be blocked there).
k8s-plugin-bundle:
	docker build --build-arg K8S_PLUGIN_VERSION=$(K8S_PLUGIN_VERSION) \
		-t $(K8S_PLUGIN_IMAGE):$(K8S_PLUGIN_TAG) -f deploy/dev/plugins.Dockerfile .
	minikube -p $(K8S_PROFILE) image load $(K8S_PLUGIN_IMAGE):$(K8S_PLUGIN_TAG)

# Start minikube, deploy the dependency stack, build baleia:dev into the node,
# then install the chart.
k8s-up:
	minikube start -p $(K8S_PROFILE) $(K8S_DRIVER_FLAG) --cpus=$(K8S_CPUS) --memory=$(K8S_MEMORY)
	kubectl apply -k deploy/dev
	minikube -p $(K8S_PROFILE) image build -t baleia:dev .
	$(MAKE) k8s-plugin-bundle
	helm upgrade --install baleia deploy/helm/baleia -n $(K8S_NS) \
		-f deploy/dev/values-dev.yaml \
		--set-string config.BALEIA_PLUGIN_VERSION=$(K8S_PLUGIN_VERSION) \
		--set trino.plugin.image=$(K8S_PLUGIN_IMAGE):$(K8S_PLUGIN_TAG) \
		--set trino.plugin.version=$(K8S_PLUGIN_VERSION) \
		--wait --timeout 10m
	# The image tag is mutable ("dev") and the chart has no checksum
	# annotation, so the upgrade leaves the pod spec unchanged and the API
	# and the worker keep running the previous image. Restart them so a
	# rebuilt image is actually deployed.
	kubectl -n $(K8S_NS) rollout restart deploy/baleia-api deploy/baleia-worker
	kubectl -n $(K8S_NS) rollout status deploy/baleia-api --timeout=5m
	kubectl -n $(K8S_NS) rollout status deploy/baleia-worker --timeout=5m
	@echo ">>> api:   kubectl -n $(K8S_NS) port-forward svc/baleia-api 8080:8080"
	@echo ">>> usage: make k8s-forward && scripts/smoke.sh"

# Port-forward the UIs for local debugging.
k8s-forward:
	kubectl -n $(K8S_NS) port-forward svc/baleia-api 8080:8080 &
	kubectl -n $(K8S_NS) port-forward svc/polaris-console 4000:80 &
	kubectl -n $(K8S_NS) port-forward svc/mailpit 8025:8025 &

k8s-logs:
	kubectl -n $(K8S_NS) logs -f -l app.kubernetes.io/name=baleia --all-containers

k8s-seed-tpch:
	kubectl -n $(K8S_NS) exec -i statefulset/postgres -- psql -U baleia -d baleia < db/seed/tpch_test.sql

# Start one source (deploy/dev/sources/<NAME>.yaml) for provider, dialect and
# directory tests; k8s-source-down removes it. `NAME=ldap` also (re)creates
# the seed ConfigMap from deploy/dev/ldap/50-baleia.ldif.
k8s-source:
	@if [ "$(NAME)" = "ldap" ]; then \
		kubectl -n $(K8S_NS) create configmap baleia-ldap-seed \
			--from-file=50-baleia.ldif=deploy/dev/ldap/50-baleia.ldif \
			--dry-run=client -o yaml | kubectl apply -f -; \
	fi
	kubectl apply -f deploy/dev/sources/$(NAME).yaml
	kubectl -n $(K8S_NS) rollout status deployment/src-$(NAME) --timeout=15m

k8s-source-down:
	kubectl delete -f deploy/dev/sources/$(NAME).yaml

# Port-forward the LDAP source so the integration test (and a manual browser
# login) can reach it at ldap://localhost:1389. The local ports are above 1024
# because a non-root kubectl cannot bind 389/636 locally.
k8s-ldap-forward:
	kubectl -n $(K8S_NS) port-forward svc/src-ldap 1389:389 1636:636 &

k8s-down:
	helm -n $(K8S_NS) uninstall baleia || true
	kubectl delete -k deploy/dev || true
	if [ -n "$${K8S_PURGE:-}" ]; then \
		minikube -p $(K8S_PROFILE) delete; \
	fi
