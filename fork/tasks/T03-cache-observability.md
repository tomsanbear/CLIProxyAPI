# T03: Measure cache hit rate per account and session

Status: open · Priority: P1 · Area: executor usage reporting, logging

## Problem

We can see sessions moving between accounts, but not how much cache we keep or lose. Every other cache task needs a before-and-after number, and the docs say the usage fields are the only reliable signal that caching works.

## Evidence

- `helps/usage_helpers.go:2697-2920` already parses `cache_read_input_tokens`, `cache_creation_input_tokens`, the 5m and 1h breakdown, and `iterations`. Nothing logs or exposes them per account or session.
- `usage-statistics-enabled` and `request-log` are both off in our config.
- Cache diagnostics need no beta header now: a request sends `diagnostics.previous_message_id`, and the response reports where the prompt diverged (model, system, tools or messages).
- With `thinking-binding-controls`, responses carry `input_transformations`, which lists thinking blocks Anthropic dropped (`thinking_dropped`, `organization_binding_mismatch`). Nothing in the proxy reads it.

## Proposed fix

1. Log one structured line per completed request: session (truncated), account, model, input, cache read, cache write 5m and 1h, output, and whether the account changed since the session's previous request.
2. Keep a per-session record of the last response id. Optionally send `diagnostics.previous_message_id` and log the divergence the response reports. The chain only works within one account, so reset it on an account switch.
3. Log `input_transformations` entries by type and path.
4. Give one summary view: read share per account and per model over a time window, either in the management API or as a script over the log.

## Done when

We can state the hit rate (read tokens over total prompt tokens) for a day of traffic. For any session with a cold write after turn 1, we can name the cause: TTL expiry, account switch, or prefix change and where it happened.

## Open questions

Adding `diagnostics` changes native request bodies. Check whether real Claude Code sends it on a custom base URL, so the proxy doesn't add a field the client never sends. The `diagnostics` object stays out of the cached prefix either way.
