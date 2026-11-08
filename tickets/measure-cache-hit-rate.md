---
id: measure-cache-hit-rate
title: Measure prompt-cache hit rate per account and session
status: todo
priority: p1
dependencies: []
related: []
scopes: [usage]
shared_scopes: [claude/executor, project/tickets]
paths: []
tags: [cache-hit-rate, observability]
---
## Goal

We can state the cache hit rate (cache-read tokens over total prompt tokens) for any day of traffic, per account, model and session. For any session with a cold write after turn 1, we can name the cause: TTL expiry, account switch, or a prefix change and where it happened.

## Gap

The logs show sessions moving between accounts but not the hit rate. Every other cache ticket needs a before-and-after number, and the docs say the usage fields are the only reliable signal that caching works.

- `internal/runtime/executor/helps/usage_helpers.go:2697-2920` already parses `cache_read_input_tokens`, `cache_creation_input_tokens`, the 5m/1h breakdown and `iterations`. Nothing logs or exposes them per account or session.
- `usage-statistics-enabled` and `request-log` are both off in our config.
- Cache diagnostics need no beta header any more: a request sends `diagnostics.previous_message_id` and the response says where the prompt diverged (model, system, tools or messages).
- With `thinking-binding-controls`, responses carry `input_transformations`, which lists thinking blocks Anthropic dropped (`thinking_dropped`, `organization_binding_mismatch`). Nothing reads it.

## Work

1. Log one structured line per completed request: session (truncated), account, model, input, cache read, cache write 5m and 1h, output, and whether the account changed since the session's previous request.
2. Keep the last response id per session. Optionally send `diagnostics.previous_message_id` and log the divergence the response reports. The chain only works within one account, so reset it on an account switch.
3. Log `input_transformations` entries by type and path.
4. One summary view: read share per account and per model over a time window (management API, or a script over the log).
5. Capture two consecutive native Claude Code request bodies (redacted) to answer: does Claude Code 2.1.319 send `thinking.block_binding`, `thread`, or `diagnostics` itself? Record the answer on `stable-thinking-config` and `message-threads-affinity`.

## Acceptance

- A day of traffic yields a hit-rate figure per account and model.
- Every cold write after turn 1 in a sample session has a named cause.
- The capture questions in Work item 5 are answered on the dependent tickets.

## Refs

- fork/context/log-evidence-2026-11-08.md, fork/context/docs-audit-2026-11-08.md
- https://platform.claude.com/docs/en/build-with-claude/cache-diagnostics
- Open question: adding `diagnostics` changes native request bodies. Check whether real Claude Code sends it on a custom base URL before the proxy adds it.
