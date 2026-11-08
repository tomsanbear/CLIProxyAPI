---
id: restart-proof-affinity
title: Keep session bindings across restarts and config reloads
status: todo
priority: p2
dependencies: []
related: [quota-aware-binding]
scopes: [routing/affinity]
shared_scopes: [project/tickets]
paths: []
tags: [cache-hit-rate, stickiness]
---
## Goal

A proxy restart or a routing config edit leaves every active session on the account it was using.

## Gap

`SessionCache` is memory-only (`sdk/cliproxy/auth/session_cache.go:31`), so every deploy (`launchctl kickstart -k`) drops all bindings. `applyManagerConfig` rebuilds the selector on any routing change (`sdk/cliproxy/service_config.go:264`), which drops them too. Afterwards each live session goes through round-robin again and has a 6-in-7 chance of landing on a cold account.

## Work

Options, which can be combined:

- Pick a new session's account by rendezvous hashing of the session id over healthy accounts, instead of round-robin. The same session maps to the same account after a restart with nothing stored, and adding or removing an account moves only the sessions that hashed to it.
- Persist the binding table to a small file in the auth directory, written periodically and loaded at startup. This keeps bindings made by failover too.
- Carry the binding cache over when the selector is rebuilt for a config change.

## Acceptance

A restart and a routing config edit both leave an active session on the same account; `measure-cache-hit-rate` shows no account change for it.

## Refs

- Rendezvous hashing balances load statistically, not strictly, and interacts with `quota-aware-binding`; a weighted hash can do both.
