# T07: Make native Claude Code detection robust

Status: open · Priority: P2 · Area: `internal/runtime/executor/helps/claude_client_detection.go`, `helps/claude_device_profile.go`

## Problem

If a genuine Claude Code request fails native detection, the proxy cloaks it without saying so. Cloaking replaces the system prompt layout, betas and cache breakpoints, so the cache prefix changes and the request no longer matches what Claude Code sent. The current checks will fail on a client update.

## Evidence

- The native user-agent pattern (`claude_client_detection.go:32`) allows only an optional `agent-sdk/x.y.z` suffix. Claude Code 2.1.319 can also send `client-app/…` and `workload/…` suffixes, according to the binary.
- The version gate (`claude_device_profile.go:218`) requires the same major and minor as the 2.1.280 baseline, with patch 280 or later. Any 2.2.x release fails it.
- Detection also requires the `claude-code-20250219` beta. The binary gates experimental betas behind `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS`; whether that removes this beta wasn't traced.
- Nothing logs a request that has a `claude-cli/` user agent but fails detection.

## Proposed fix

1. Accept any well-formed set of suffixes in the user agent.
2. Replace the major.minor pin with a lower bound, so newer versions pass.
3. Log a warning, rate-limited, when a request with a `claude-cli/` user agent is cloaked, with the reason it failed.
4. Update the cloaking baseline profile to current values: user agent, stainless package 0.128.0, and the runtime version.

## Done when

Tests show 2.1.319 user agents with each suffix, and a 2.2.0 user agent, pass detection. A request that fails detection produces one warning naming the failed check.

## Open questions

How far ahead to trust unknown future versions. A lower bound alone accepts anything newer, which is the right failure mode for a passthrough.
