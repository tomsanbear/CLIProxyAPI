# T10: Fix API gaps on the cloaked and translated paths

Status: open · Priority: P3 · Area: Claude executor, registry

## Problem

These gaps don't touch native Claude Code traffic. They affect other clients (OpenAI- or Gemini-format, non-native Anthropic) that the proxy cloaks or translates, and they can produce 400s or stale behaviour.

## Items

| Gap | Where | Effect |
|---|---|---|
| A top-level `cache_control` (automatic caching) isn't seen by breakpoint counting, the 4-slot cap, or the TTL passes. | `claude_executor_cloaking.go:3610-3644`, `:3890` | Five breakpoints, or a 1h-after-5m ordering violation, give a 400. |
| Explicit cache mode skips TTL ordering normalisation. | `claude_executor_execute.go:~200` | A caller sending 5m then 1h gets a 400 instead of a downgrade. |
| Forced `tool_choice` (`any` or `tool`) isn't rewritten for Opus 5.5, Fable 5.1 or Sonnet 5.5, which reject it. | `claude_executor_request.go:942-990`; the translators emit `any` for `required` | 400 for translated clients. |
| `fallbacks: "default"` gets the `server-side-fallback-2026-06-01` beta, but needs `-2026-07-01`. | `claude_executor_request.go:46`, `:229` | 400 if a client uses the `"default"` form. The injected array form is correct. |
| The fallback-credit beta is `fallback-credit-2026-06-01`, but the docs name `-2026-07-01`. | `claude_executor_request.go:47`, `:232` | Unconfirmed. Check a live capture before changing it. |
| Cost accounting uses only top-level `usage`, which covers the final attempt. Refused attempts in `usage.iterations` aren't added. | `helps/usage_helpers.go:2748` | Undercounted usage after a fallback. |
| The cloaking profile is stale: user agent 2.1.280, stainless 0.112.1, and newer betas missing. | `helps/claude_device_profile.go:23-27`, `claude_executor_request.go:173-278` | Cloaked traffic looks like an old client. This overlaps [T07](T07-native-detection.md). |
| The registry still lists retired Opus 4 and Opus 4.1, and Sonnet 4.5, which retires 2026-11-30. | `internal/registry/models/models.json:26`, `:402`, `:423` | Model listings advertise models that fail. |
| Mid-conversation system messages are treated as supported on Sonnet 5, but the docs say they aren't. | `claude_executor_cloaking.go:949-970`, `:1059` | Possible 400 when cloaking moves the system prompt on Sonnet 5. The code comment cites native captures, so check before changing it. |
| Fast mode (`speed: fast`) has no model gate, and the `anthropic-fast-*` headers aren't used in cooldown logic. | `claude_executor_request.go:634`, `:1286` | An upstream error on models without fast mode, and a generic 429 handling for fast-mode limits. |

## Done when

Each row is fixed with a regression test or closed with a reason. Split rows into their own task files if any grows beyond a small change.
