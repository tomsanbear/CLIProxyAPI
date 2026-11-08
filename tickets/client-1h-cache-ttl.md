---
id: client-1h-cache-ttl
title: Turn on the 1-hour cache TTL in Claude Code
status: todo
priority: p1
dependencies: []
related: [measure-cache-hit-rate]
scopes: []
shared_scopes: [fork/docs, project/tickets]
paths: []
tags: [cache-hit-rate, config]
---
## Goal

Our Claude Code sends 1-hour cache breakpoints, so pauses and long turns don't expire the conversation cache.

## Gap

Claude Code 2.1.319 sends 5-minute breakpoints for us. A cache entry's lifetime starts when the request starts, so a 4-minute Opus or Fable turn leaves about a minute for the next request. Any pause over 5 minutes, ours or a long tool run's, makes the next request rewrite the whole conversation at write price.

- The binary says the TTL is "5 minutes unless ENABLE_PROMPT_CACHING_1H=1". Logged-in subscribers on an allowlist also get 1h. `CLAUDE_CODE_PROMPT_CACHE_TTL` and `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL` override it.
- `~/.claude/settings.json` authenticates through `apiKeyHelper` and sets none of these.
- On native requests the proxy keeps the client's TTLs. `upgradeClaudeCacheControlTTLDoc` runs only when the proxy owns breakpoint placement (`claude_executor_execute.go:187`), and the `extended-cache-ttl` beta it adds does nothing without `ttl: "1h"` in the body.

## Work

Add `"ENABLE_PROMPT_CACHING_1H": "1"` to the `env` block of `~/.claude/settings.json` (owner's global config: the owner applies it or approves the edit). Note the change in FORK.md. A proxy rule upgrading native TTLs would be the alternative, but it means the proxy rewriting a native body.

Trade-off: a 1-hour write costs 2x base input instead of 1.25x, and a read refreshes either TTL for free. 1h pays off when requests sharing a prefix start 5 to 60 minutes apart. On Fable 5.1, cache reads cost 0.025x base, so the docs suggest a `max_tokens: 0` keep-alive instead of the 1h TTL.

## Acceptance

- The setting is in place.
- `measure-cache-hit-rate` shows `ephemeral_1h_input_tokens` in cache writes, and read share rises for sessions with gaps over 5 minutes.

## Refs

- https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- Open questions: how a 1h write counts against an account's 5h and 7d subscription windows; when Claude Code uses the undocumented `cache-keepalive-2026-09-03` beta found in the binary.
