# T06: Keep session bindings across restarts and config reloads

Status: open · Priority: P2 · Area: `sdk/cliproxy/auth/selector.go`, `session_cache.go`, `sdk/cliproxy/service_config.go`

## Problem

Bindings are lost in two cases:

- Every proxy restart. Deploys run `launchctl kickstart -k`, and the in-memory binding table goes with the process.
- Every routing config change, because that rebuilds the selector.

After either one, each live session is assigned by round-robin again and has a 6-in-7 chance of landing on a cold account.

## Evidence

- `SessionCache` is memory-only (`session_cache.go:31`).
- `applyManagerConfig` calls `SetSelector(newRoutingSelector(...))` whenever the routing state changes (`service_config.go:264`).

## Proposed fix

There are two options, and they can be combined:

- **Deterministic first binding.** Pick a new session's account by rendezvous hashing of the session id over healthy accounts, instead of round-robin. A session lands on the same account after a restart with nothing stored, and adding or removing an account only moves the sessions that hashed to it.
- **Persist the binding table** to a small file in the auth directory, written periodically and loaded at startup. This also keeps bindings that came from failover. It costs a file write path and a staleness question.

Also carry the binding cache over when the selector is rebuilt for a config change, instead of starting empty.

## Done when

A restart, and a routing config edit, both leave an active session on the same account. The [T03](T03-cache-observability.md) logs show no account change for that session.

## Open questions

- Rendezvous hashing balances load statistically rather than strictly. That's acceptable with many sessions, but it interacts with [T08](T08-quota-aware-binding.md); a weighted hash can do both.
- Bindings made by failover differ from the hash result. Persisting them, or not, is a design choice.
