# T08: Bind new sessions to accounts with quota headroom

Status: open · Priority: P3 · Area: `sdk/cliproxy/auth/selector.go`, `quota_signals.go`

## Problem

New sessions are assigned by round-robin and ignore how much of its 5h and 7d windows each account has used. A session can bind to an account that is nearly exhausted, hit a 429 mid-conversation, and rebind cold. Choosing a better account at binding time costs no cache, because nothing is warm yet.

## Evidence

- `quota_signals.go:152-176` records the `anthropic-ratelimit-unified-*` headers, but only for display.
- The selector's availability check (`selector.go:824`) uses only cooldown, quota-exceeded and disabled state.
- The logs show about 30 real 429s over a week, so the problem is real but small next to [T02](T02-failover-rebinding.md).

## Proposed fix

When creating a binding, prefer accounts with the lowest utilisation of the window that resets latest. Existing bindings stay where they are until a real 429.

## Done when

Over a test period, sessions bound to accounts above a utilisation threshold hit fewer mid-session 429 rebinds.

## Open questions

- The unified headers are undocumented, so their meaning may drift.
- This interacts with deterministic hashing in [T06](T06-restart-proof-affinity.md); one design should cover both.
