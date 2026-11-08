---
id: quota-aware-binding
title: Bind new sessions to accounts with quota headroom
status: todo
priority: p3
dependencies: []
related: [restart-proof-affinity]
scopes: [routing/affinity]
shared_scopes: [project/tickets]
paths: []
tags: [cache-hit-rate, stickiness]
---
## Goal

New sessions bind to accounts with headroom in their 5h and 7d windows, so fewer sessions hit a 429 mid-conversation and rebind cold.

## Gap

Round-robin ignores account utilisation. `sdk/cliproxy/auth/quota_signals.go:152-176` records the `anthropic-ratelimit-unified-*` headers, but only for display; the selector's availability check (`selector.go:824`) uses only cooldown, quota-exceeded and disabled state. The logs show about 30 real 429s in a week, so the cost is real but small next to `failover-keeps-session-binding`.

## Work

When creating a binding, prefer accounts with the lowest utilisation of the window that resets latest. Existing bindings stay put until a real 429.

## Acceptance

Over a test period, sessions bound to accounts above a utilisation threshold hit fewer mid-session 429 rebinds.

## Refs

- The unified headers are undocumented, so their meaning may drift.
- Design together with `restart-proof-affinity`.
