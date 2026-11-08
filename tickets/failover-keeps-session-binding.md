---
id: failover-keeps-session-binding
title: Stop transient failures from moving sessions to another account
status: todo
priority: p1
dependencies: []
related: []
scopes: [routing/affinity, routing/conductor]
shared_scopes: [project/tickets]
paths: []
tags: [cache-hit-rate, stickiness]
---
## Goal

A failure that isn't the bound account's fault (a local DNS outage, a dropped connection) never moves a session to another account.

## Gap

The conductor tries the next account and the affinity selector permanently re-points the session at whichever account answers. When the network returns, the session starts cold elsewhere. On Sonnet 5.5 the move also silently discards earlier reasoning, because its thinking blocks are bound to the producing account.

- Logs: 474 reselections over a week, clustered in the hours with local network failures (fork/context/log-evidence-2026-11-08.md).
- Transport errors don't cool the account: `shouldSkipCredentialCooldown`, `sdk/cliproxy/auth/conductor_cooldown.go:2014`, with the message matcher at `:2179`.
- The conductor drops accounts already tried in this request from the candidate list (`conductor_selection.go:1952`).
- `SessionAffinitySelector.Pick` then sees the bound account as unavailable and calls `bind(newAuth, true)` (`selector.go:1205-1222`).
- `rebind-on: quota` keeps the binding via `transientlyUnavailableSessionAffinityAuthIDs` (`conductor_selection.go:685`), but our config uses the default `any`.

## Work

1. Config: set `routing.session-affinity-rebind-on: quota`. Applying it rebuilds the selector, which drops current bindings once.
2. Code: an account the current request already tried is not a reason to rebind. Rebind only when the bound account itself is blocked by quota or auth state, so `any` behaves correctly too.
3. Code: when every account fails with the same transport error inside one round, stop walking the pool and return the error.

## Acceptance

- A simulated DNS failure followed by recovery leaves the session bound to its original account, covered by a regression test with a fake transport error.
- `reselected` log lines appear only for 429, auth or quota failures.

## Refs

- Open question: whether Work item 3 applies to all transport errors or only to clearly local ones (DNS lookup, network unreachable).
