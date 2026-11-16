# Error surfaces

Every async screen, modal, form and toast follows the same error taxonomy. The
API sends a stable `code` (plus optional `params`, `fields` and `request_id`);
the UI maps it to localized copy. A raw engine or API message is never the
headline (FD7).

## Taxonomy

| Family | Where it appears | Surface |
| --- | --- | --- |
| Validation | Forms | Inline field error, focus the first invalid field |
| Permission (403) | Any protected view | Page-level `ErrorState` without leaking existence |
| Not found (404) | Routes and resources | `NotFound` / `ErrorState` with recovery |
| Conflict | Mutations | Inline explanation and a way to reload |
| Network/offline | Any request | Retryable `ErrorState` |
| Session expired | Any request | `SessionExpired` modal with the return URL |
| Async operation | Runs, maintenance, syncs | `StateTimeline` with retry/resume |
| Engine/query | Workbench, Explore | `ErrorPanel` with the catalog headline and collapsible native detail |
| Unexpected server | Any request | Safe headline, request ID and retry |

## Rules

- Use `apiErrorMessage(error, fallback)` for a message string. It localizes a
  known code and never renders `[object Object]` for a thrown API body.
- Use `describeError(error)` and `ErrorPanel` when the headline matters.
- Show the stable `code` and `request_id` when the API provides them.
- Recoverable failures offer retry; durable failures stay visible instead of a
  transient toast. Success and info toasts dismiss themselves; an error toast
  stays until the user dismisses it.
- Empty `catch` blocks are a lint error.

## Surface inventory

| Area | Representative async views | Main surface |
| --- | --- | --- |
| Sign-in and setup | Login, SSO, reset password, first-run setup | Inline form error + toast for background failures |
| Workbench | Execution, cancellation, saved queries, results | `ErrorPanel` / `StateTimeline` |
| Data Explorer | Catalog, schema, table and column loads | `ErrorState` with retry |
| Explore | Field catalog, compile and run | `ErrorPanel` with the engine detail collapsed |
| Content | Models, metrics, glossary, docs, folders | Inline form error + `ErrorState` |
| Connections | Connections, providers, catalogs, bindings | `ErrorState` + `ErrorPanel` |
| Operations | Monitoring, maintenance, history | `ErrorState`, `StateTimeline`, request ID |
| Administration | Workspace, users, teams, identity, service accounts | Inline error + `ConfirmDialog` consequences |
| Assistant | Agents, retrievers, assistant settings | `ErrorState` + disabled/quota states |

BI gateway flows are tracked separately with #1065.

## Contributor checklist

1. Classify the failure with the taxonomy above.
2. Render it with the matching surface, not a generic modal.
3. Use `apiErrorMessage`/`describeError`; never show `e.message` directly.
4. Provide loading, empty, error and retry states for the async view.
5. Add a page test with a typed MSW handler for the failure.
