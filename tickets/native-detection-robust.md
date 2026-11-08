---
id: native-detection-robust
title: Make native Claude Code detection robust to client updates
status: todo
priority: p2
dependencies: []
related: []
scopes: [claude/helpers]
shared_scopes: [project/tickets]
paths: []
tags: [cache-hit-rate, invisibility]
---
## Goal

Every genuine Claude Code request is passed through natively, including future versions, and a failed detection is logged.

## Gap

A genuine request that fails detection is silently cloaked: system prompt layout, betas and cache breakpoints change, so the cache prefix changes and the request no longer matches what Claude Code sent.

- The native user-agent pattern (`internal/runtime/executor/helps/claude_client_detection.go:32`) allows only an optional `agent-sdk/x.y.z` suffix. The 2.1.319 binary can also send `client-app/...` and `workload/...` suffixes.
- The version gate (`helps/claude_device_profile.go:218`) needs the same major.minor as the 2.1.280 baseline with patch 280 or later, so any 2.2.x fails.
- Detection also requires the `claude-code-20250219` beta, which the binary gates behind `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS` (not traced; our env doesn't set it).
- Nothing logs a `claude-cli/` request that fails detection.

## Work

1. Accept any well-formed set of user-agent suffixes.
2. Replace the major.minor pin with a lower bound.
3. Log a rate-limited warning naming the failed check when a `claude-cli/` request is cloaked.
4. Refresh the cloaking baseline profile: user agent, stainless package 0.128.0, runtime version.

## Acceptance

Tests pass detection for 2.1.319 user agents with each suffix and for a 2.2.0 user agent. A failed detection produces one warning naming the check.

## Refs

- fork/context/docs-audit-2026-11-08.md (Claude Code binary findings)
