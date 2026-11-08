# T09: Support message threads across accounts

Status: open · Priority: P3 · Area: affinity, `internal/clienterror/client_error.go`, Claude executor

## Problem

With message threads (`message-threads-2026-08-12`), Anthropic keeps the conversation server-side per organization. A request continuing a thread only works on the account that created it. The proxy has no thread-to-account pinning, so a failover, restart or rebind sends the continuation to the wrong account and gets a 404.

The 404 is detected by message text rather than by its error code. If the wording differs from what the proxy expects, the 404 cools the new account and model for 12 hours.

## Evidence

- The docs say an unknown thread returns 404 `not_found_error` with `error.details.error_code: "thread_not_found"`, and that the message text is deliberately vague.
- `IsClaudeThreadNotFound` (`client_error.go:153`) requires the message to contain both "thread state" and "previous_message_id".
- An unmatched 404 gets `Retry-After` or 12h (`conductor_cooldown.go:1110-1118`).
- count_tokens doesn't strip `thread`, which the docs say is a 400 (`claude_executor_tokens.go:237-251`).
- `enforceCacheControlLimitDoc` reserves one breakpoint for a thread, but doesn't count the top-level `cache_control` or the breakpoints stored with the thread (`claude_executor_cloaking.go:3895`).
- The 2.1.319 binary contains the beta and a `thread: {type: create|continue}` field. It isn't known whether Claude Code sends them by default.

## Proposed fix

1. Match thread-not-found on `error.details.error_code` as well as the current text check.
2. Pin a thread continuation to the account that created it. Return a clear request-scoped error if that account is unavailable, so the client replays the conversation.
3. Strip `thread` on count_tokens.

## Done when

Tests show a cross-account thread 404 cooling nothing and passing to the client as a request error, and a thread continuation routed to its creating account.

## Open questions

Check whether Claude Code 2.1.319 sends `thread` on our traffic before deciding priority. If it does, this becomes P1.
