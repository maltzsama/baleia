# AGENTS.md

Guidance for agents and contributors working in this repository. The backend
rules follow the ADRs under `docs/adr/`; the frontend rules below are the ones
the phase-3 issues reference as **FD1–FD16**.

## Repository commands

| Command | What it does |
|---|---|
| `make dev` / `make worker` | Run the API / Temporal worker locally. |
| `make up` / `make down` | Start / stop the compose stack. |
| `make generate` / `make sqlc` | Regenerate OpenAPI handlers / SQL code. |
| `make lint` / `make lint-all` | `golangci-lint` on what is new since `origin/main` / on everything. |
| `make vuln` | `govulncheck`. |
| `make build` / `build-web` / `build-all` | Compile binaries / frontend / both. |
| `make e2e` | minikube (`make k8s-up`) + `cmd/e2e-seed` + Playwright (`web/e2e`). |
| `make e2e-down` | Tear minikube down (`k8s-down` with `K8S_PURGE=1`). |

Backend definition of done (`DoD-standard`): `go vet ./...`, `go test -race
./...` (with `BALEIA_TEST_DATABASE_URL` for the DB-backed tests), `make lint`,
`make vuln`, regenerated OpenAPI/sqlc when the contract changes, and a Bruno
case for every new endpoint. CI (`.github/workflows/ci.yml`) runs the same:
`Test` with Postgres and `-race`, `Lint` (golangci-lint, `.golangci.yml`, new
issues only) and `Vulnerabilities` (govulncheck).

## Frontend

The SPA lives in `web/` (SvelteKit, Svelte 5 runes, Tailwind v4, adapter-static).

### Definition of done (`DoD-web`)

From `web/`:

```bash
npm run check        # svelte-kit sync + svelte-check (strict)
npm run lint         # eslint flat config
npm run format:check # prettier
npm test             # vitest + testing-library + MSW
npm run build        # adapter-static
```

CI (`.github/workflows/ci.yml`, job `web`) runs all five. The Playwright suite
runs in `.github/workflows/web-e2e.yml` for PRs that touch `web/**`,
`api/swagger.yaml` or `cmd/e2e-seed/**`, and nightly on `main`.

### Info architecture

```
/sql                  Workbench (editor + explorer + results)
/sql/q/{query_id}     Workbench with a saved query open
/data                 Data explorer (metadata-driven)
/explore?q=           Semantic explore (model fields -> compiled SQL, FD5 share link)
/models               Semantic models; /models/{model_id} is the model editor
/connections          Connections, catalogs and bindings
/monitoring           Cluster, queries, table health and maintenance
/history              Query history
/settings/*           Settings and administration
```

`/datasources` and `/lens` redirect to `/connections` and `/monitoring`.
Workspace/catalog/namespace/table/tab selection is URL-backed through
`src/lib/stores/selection.svelte.ts` (`?ws=&catalog=&ns=&tab=`).

### State

- Typed stores only (`src/lib/stores/`): `auth`, `workspace`, `editor`,
  `savedQueries`, `selection`. Never use `window.dispatchEvent` or
  `window.addEventListener` for app state; a lint rule enforces this.
- Every API call goes through `src/lib/api/client.ts` (typed from
  `api/swagger.yaml`). `apiFetch` is only for non-JSON responses (Arrow) and
  the legacy explorer routes that #23 replaces.
- Regenerate types with `npm run gen:api`; CI fails if the checked-in
  `src/lib/api/schema.d.ts` is stale.
- Authorization comes from `allowed_actions` on resources and
  `global_actions` on `/auth/me` (helpers in `src/lib/authz/can.ts`). Never
  compare roles in the client.
- User-facing text goes through Paraglide: `import { m } from
  '$lib/paraglide/messages'`. `npm run check` enforces key parity between
  `messages/en.json` and `messages/pt-BR.json`; the generated
  `src/lib/paraglide/` is never edited or committed.

### UI layers

- `src/lib/ui/` — primitives: `Button`, `Input`, `Textarea`, `Select`,
  `Checkbox`, `Modal`, `Drawer`, `Table`, `Badge`, `Tabs`, `Skeleton`,
  `EmptyState`, `ErrorState`, `Toast`/`Toaster`.
- `src/lib/ui/patterns/` — product patterns: `StatusBadge`, `KindIcon`,
  `EngineBadge`, `ConfirmDialog`, `KeyValueEditor`, `FilterBar`, `DataTable`,
  `RelativeTime`, `Duration`, `CopyButton`, `CodeBlock`, `DiffView`,
  `MarkdownView`, `MarkdownEditor`, `OneTimeSecret`, `ErrorPanel`,
  `StateTimeline`.
- `src/lib/util/poll.ts`, `src/lib/util/time.ts`, `src/lib/errors/catalog.ts`.
- Provider/engine kind literals live only in `ui/patterns/kindIcon.ts` (FD6).
- Content kinds (query, metric, model) get their label and route from
  `src/lib/content/kinds.ts`; a new kind is one more entry.
- The explore query (`src/lib/semantic/explore.ts`) is the only Explore state:
  it is serialized into `?q=` as base64url of canonical JSON (FD5) and the UI
  never sends raw SQL to the compiler.
- SQL shown read-only goes through `CodeBlock` (named, focusable, AA syntax colors
  from `ui/patterns/codeHighlight.ts`), with the engine's `codemirror_base`.
- Workbench autocomplete: `src/lib/workbench/sqlContext.ts` says what fits at
  the cursor (statement keyword, table, column, clause keyword);
  `sqlCompletion.ts` fills it from the tab catalog's stored metadata, by prefix,
  never from a workspace-wide search; `completionTheme.ts` styles the popup.
- Markdown is rendered only through `MarkdownView` (sanitized, images never
  rendered).
- Every async view has loading, empty and error states; empty `catch` blocks
  are a lint error.
- Dev-only pattern gallery: `/_dev/patterns` (never prerendered, dynamically
  imported).

### Page tests (MSW)

- `src/test/server.ts` creates typed handlers with `openapi-msw` and a
  `baseUrl` of `/api/v1`; `src/test/setup.ts` starts the server with
  `onUnhandledRequest: 'error'`.
- `src/test/factories.ts` has a factory per schema (`makeWorkspace`,
  `makeSavedQuery`, `makeEngine`, `makeMe`, `asViewer`, `asOwner`).
- `src/test/render.ts` exports `renderPage(Component, { me, url })` to mount a
  route with the auth store and URL initialized.
- **An unmocked request fails the test.** Add a handler instead of stubbing
  `fetch`.
- A handler whose response does not match the OpenAPI schema fails
  `npm run check`.

```ts
import { http, server } from '$lib/test/server';
import { makeWorkspace } from '$lib/test/factories';

server.use(http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])));
```

### Smoke tests (Playwright)

- `web/e2e/` runs against the **minikube dev cluster** (not compose);
  `make e2e` runs `k8s-up`, port-forwards the API/Postgres/Trino, seeds with
  `cmd/e2e-seed` and runs the suite. `make e2e-down` purges the cluster.
- Tag every spec with `@issue-<number>` in the title and run one with
  `npm run e2e -- --grep @issue-118`.
- `loginAs('admin' | 'member' | 'viewer')` and
  `expectNoA11yViolations(page)` come from `e2e/fixtures.ts`.
- Seeded accounts: `admin@e2e.test`, `member@e2e.test`, `viewer@e2e.test`
  with `E2E_PASSWORD` (default `e2e-password`).
- Never seed through raw SQL; extend `cmd/e2e-seed` (build tag `e2e`).
- Do not depend on execution order across spec files.

### Frontend rules referenced by issues

- **FD1** — frontend stays in this repository's `web/`.
- **FD3** — content is space-scoped; saved queries and metrics carry a target.
- **FD4** — destructive actions require confirmation with consequences.
- **FD6** — provider/engine kind literals only in `kindIcon.ts`.
- **FD7** — errors are rendered from `errors/catalog.ts`; never show a raw
  engine message as the headline.
- **FD8** — secrets are shown once, never stored in a store or URL.
- **FD9** — authorization uses `allowed_actions`/`global_actions`.
- **FD10** — polling uses `util/poll.ts` (pauses when hidden, bounded).
- **FD11** — every interactive element is keyboard accessible and labelled.
- **FD13** — timestamps show relative time with an absolute tooltip.
- **FD14** — typed mock handlers and `renderPage` for page tests.
- **FD16** — content-as-code: upsert by slug, never delete omitted items.

## Backend conventions

- The OpenAPI spec (`api/swagger.yaml`) is the source of truth; regenerate
  with `make generate` and `make sqlc`.
- `internal/api.API` composes one handler group per domain (`AuthHandlers`,
  `WorkspaceHandlers`, `CatalogHandlers`, `QueryHandlers`, `SemanticHandlers`,
  `AgentHandlers`, `OpsHandlers`) over a shared `core` (database, engines,
  audit, settings). A new operation goes on its domain's group; state only
  that group needs is a field of the group, not of `core`. Tests build the
  API with `testAPI(handlerFields{...})`.
- Every credential write goes through the keyring; decrypt fails closed.
- Every outbound connection goes through `connections.Dialer`.
- Audit events are best-effort and append-only; versions are transactional.
- No raw SQL in commands: use the generated queries or the service layer.
- API errors use the closed `ErrorCode` enum in `api/swagger.yaml`. A new code
  must be added there and as `error_<code>` in `web/messages/{en,pt-BR}.json`;
  `TestErrorCodesClosed` fails otherwise. Field errors use `FieldCode`
  (`field_<code>`). The server sends a stable `code` + `params`; only e-mail is
  localized server-side.
