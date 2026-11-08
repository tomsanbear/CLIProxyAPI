# T04: Handle 529 and mid-stream errors correctly

Status: open · Priority: P1 · Area: `sdk/cliproxy/auth/conductor_cooldown.go`, `conductor_selection.go`, Claude executor

## Problem

The proxy misclassifies two kinds of upstream failure:

- **529 `overloaded_error`.** It is in neither the transient cooldown list nor the retry-round statuses. It gets a flat 1-minute cooldown and ignores `retry-after`. Under the current `rebind-on: any` the session moves to another account. Overload is Anthropic-wide, so another account doesn't help and the move only throws the warm cache away.
- **Mid-stream `event: error`.** Every SSE error event that arrives after HTTP 200 becomes a 502, so the error type and any retry hint are lost.

## Evidence

- `conductor_cooldown.go:1252` lists `408, 500, 502, 503, 504, 520-526`. 529 falls to `default:` at `:1255`, which calls `recoverableFailureRetryAfter` without the hint.
- `isCredentialRetryRoundStatus` (`conductor_selection.go:1564`) omits 529.
- `internal/runtime/executor/claude_executor_diagnostics.go:162` sets `statusErr{code: 502}` for any `type == "error"` event.
- The errors doc lists 529, 500 and 504 as retryable and tells clients to honour `retry-after`.

## Proposed fix

1. Add 529 to the transient case list and to the retry-round statuses.
2. Map mid-stream error types to their statuses: `overloaded_error` to 529, `api_error` to 500, `timeout_error` to 504, `rate_limit_error` to 429. Keep the original body.
3. For errors that affect every account (529, and 5xx without account-specific signals), wait for `retry-after` and retry the same account rather than failing over. This is a policy change. Bound it by `max-retry-interval` so a long overload still surfaces to the client.

## Done when

Tests cover a 529 with and without `retry-after`, and each mid-stream error type. A 529 no longer changes the session's binding.

## Open questions

Whether a single 500 `api_error` should cool the model at all, or only retry. The docs treat 500 as retry-the-same-request.
