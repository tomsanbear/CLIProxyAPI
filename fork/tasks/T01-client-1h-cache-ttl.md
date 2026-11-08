# T01: Turn on the 1-hour cache TTL in Claude Code

Status: open · Priority: P1 · Area: client config (`~/.claude/settings.json`), no proxy code

## Problem

Our Claude Code sends 5-minute cache breakpoints. A cache entry's lifetime starts when the request starts, so a 4-minute Opus or Fable turn leaves about a minute for the next request. Any pause over 5 minutes, ours or a long tool run's, makes the next request rewrite the whole conversation at write price.

## Evidence

- The 2.1.319 binary says the TTL is "5 minutes unless ENABLE_PROMPT_CACHING_1H=1". Logged-in subscribers on an allowlist also get 1h. `CLAUDE_CODE_PROMPT_CACHE_TTL` and `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL` override it.
- `~/.claude/settings.json` authenticates through `apiKeyHelper` and sets none of these variables.
- On native requests the proxy keeps the client's breakpoints and TTLs. `upgradeClaudeCacheControlTTLDoc` runs only when the proxy owns breakpoint placement (`internal/runtime/executor/claude_executor_execute.go:187`). The `extended-cache-ttl` beta it adds has no effect without `ttl: "1h"` in the body.

## Proposed fix

Add `"ENABLE_PROMPT_CACHING_1H": "1"` to the `env` block in `~/.claude/settings.json`. The alternative is a proxy rule that upgrades native TTLs to 1h. That would mean the proxy rewriting a native body, and the client setting is simpler.

## Trade-off

A 1-hour write costs 2× base input instead of 1.25×, and a read refreshes either TTL for free. 1h pays off when requests sharing a prefix start 5 to 60 minutes apart, which describes interactive sessions with pauses and long turns. On Fable 5.1, cache reads cost 0.025× base, so the docs suggest a `max_tokens: 0` keep-alive instead of the 1h TTL.

## Done when

- The setting is in place.
- [T03](T03-cache-observability.md) shows `ephemeral_1h_input_tokens` in cache writes.
- Read share rises for sessions with gaps longer than 5 minutes.

## Open questions

- Subscription limits count tokens differently from API billing. It isn't known how a 1h write counts against an account's 5h and 7d windows.
- The binary also contains a `cache-keepalive-2026-09-03` beta. It is undocumented, and it isn't known when Claude Code uses it.
