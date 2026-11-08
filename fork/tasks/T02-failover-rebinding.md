# T02: Stop transient failures from moving sessions to another account

Status: open · Priority: P1 · Area: `sdk/cliproxy/auth` (selector, conductor), proxy config

## Problem

When a request fails on its bound account for a reason that has nothing to do with that account, such as a local DNS outage, the conductor tries the next account. The affinity selector then permanently re-points the session at whichever account answers. When the network returns, the session starts cold on a different account. On Sonnet 5.5 the move also silently discards all earlier reasoning, because its thinking blocks are bound to the account that produced them.

## Evidence

- [Log evidence](../context/log-evidence-2026-11-08.md): 474 reselections, clustered in the hours with local network failures.
- Transport errors don't cool the account: `shouldSkipCredentialCooldown` in `sdk/cliproxy/auth/conductor_cooldown.go:2014`, with the message matcher at `:2179`.
- The conductor drops accounts already tried in this request from the candidate list (`conductor_selection.go:1952`).
- `SessionAffinitySelector.Pick` then sees the bound account as unavailable and calls `bind(newAuth, true)` (`selector.go:1205-1222`).
- With `rebind-on: quota`, `transientlyUnavailableSessionAffinityAuthIDs` (`conductor_selection.go:685`) keeps the binding, but our config uses the default `any`.

## Proposed fix

1. Config: set `routing.session-affinity-rebind-on: quota`. Applying it rebuilds the selector, which drops current bindings once.
2. Code: don't count an account the current request already tried as a reason to rebind. Rebind only when the bound account itself is blocked by quota or auth state. This makes `any` behave correctly too.
3. Code: when every account fails with the same transport error inside one round, stop walking the pool and return the error. Cycling 7 accounts during a DNS outage achieves nothing.

## Done when

- A simulated DNS failure followed by recovery leaves the session bound to its original account. A regression test with a fake transport error covers it.
- `reselected` log lines appear only for 429, auth or quota failures.

## Open questions

Whether part 3 should apply to all transport errors or only to DNS lookup and network-unreachable errors, which are clearly local.
