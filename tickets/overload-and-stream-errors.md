---
id: overload-and-stream-errors
title: Handle 529 overload and mid-stream errors without switching accounts
status: todo
priority: p1
dependencies: []
related: [failover-keeps-session-binding]
scopes: [routing/conductor, claude/executor]
shared_scopes: [project/tickets]
paths: []
tags: [cache-hit-rate, stickiness, errors]
---
## Goal

Anthropic-wide errors are retried on the session's own account with the server's `retry-after`, and errors inside a stream keep their real type.

## Gap

- 529 `overloaded_error` is in neither the transient cooldown list (`conductor_cooldown.go:1252`, falls to `default:` at `:1255` without the retry hint) nor the retry-round statuses (`isCredentialRetryRoundStatus`, `conductor_selection.go:1564`). It gets a flat 1-minute cooldown, ignores `retry-after`, and under `rebind-on: any` moves the session. Overload is Anthropic-wide, so another account can't help.
- Every SSE `error` event after HTTP 200 becomes a 502 (`internal/runtime/executor/claude_executor_diagnostics.go:162`), so the error type and retry hint are lost.
- The errors doc lists 529, 500 and 504 as retryable and tells clients to honour `retry-after`.

## Work

1. Add 529 to the transient case list and the retry-round statuses.
2. Map mid-stream error types to statuses: `overloaded_error` 529, `api_error` 500, `timeout_error` 504, `rate_limit_error` 429. Keep the original body.
3. For errors that affect every account (529, and 5xx without account-specific signals), wait for `retry-after` and retry the same account instead of failing over, bounded by `max-retry-interval` so a long overload still reaches the client.

## Acceptance

Tests cover a 529 with and without `retry-after` and each mid-stream error type. A 529 no longer changes the session's binding.

## Refs

- https://platform.claude.com/docs/en/api/errors
- Open question: whether a single 500 `api_error` should cool the model at all, or only retry.
