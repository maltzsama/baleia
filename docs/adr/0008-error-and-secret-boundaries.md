# 0008. Error and secret boundaries

- Status: Accepted
- Date: 2026-10-14
- Issues: #9, #10, #20, #46, #67

## Context

Engine and driver errors carry SQL text, hostnames, and sometimes credentials. Forwarding them to
API clients or logs leaks information, and free-form messages cannot be localized (#67) or handled
programmatically.

## Decision

- No error that crosses the API boundary carries SQL text, credentials or a raw engine message.
- Engines return **typed errors with a bounded `code`** (a closed set defined by the engine contract,
  #20); the API maps codes to responses and messages.
- Secrets are never logged and never returned (#10). Request logs never include bodies, headers or
  query strings (#46).
- Result values are never logged.

## Consequences

- A user sees a stable error code and a safe message; details stay in the engine's own logs.
- Localized messages are keyed by error code.
- Debugging needs the correlation id (#46) to find
  the engine-side record.

## Current compliance

Partially compliant.

- Secrets are not returned (#10) and request logs exclude bodies, headers and query strings (#46).
- Statement errors store and return the engine's message (`ErrorMessage` in
  `internal/temporal/activities/activities.go`) until typed engine errors land (#20).
- Some handlers return `err.Error()` in 4xx bodies (e.g. `internal/api/handlers_retrievers.go`);
  these are replaced by closed error codes (#67).

## Alternatives considered

- **Pass engine messages through, sanitized by regex.** Rejected: sanitizing unknown formats is
  unreliable.
