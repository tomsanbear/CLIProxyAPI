# How the proxy works

Written 2026-11-08 against commit `511ede8b`. Covers the path our Claude Code traffic takes: `POST /v1/messages` to Claude OAuth accounts.

## Request path

1. Gin routes `/v1/messages` and `/v1/messages/count_tokens` through the access-key middleware (`internal/api/server_middleware.go:502`) to `ClaudeMessages` (`sdk/api/handlers/claude/code_handlers.go:72`). The body is passed on as received.
2. The base handler builds the executor request and forwards the client's headers in `opts.Headers` (`sdk/api/handlers/handlers_execution.go:34`).
3. The conductor loop (`sdk/cliproxy/auth/conductor_execution.go:513`) asks the selector for an account, refreshes its token if needed, and calls the executor. On a credential failure it moves to the next untried account in the same round.
4. The Claude executor (`internal/runtime/executor/claude_executor_execute.go`, `claude_executor_stream.go`) prepares the body and headers and sends them to `https://api.anthropic.com/v1/messages?beta=true` over a uTLS client. Claude-to-Claude has no translation step.
5. Streaming events are relayed as received, with aliased tool names mapped back (`code_handlers.go:223-475`). Upstream response headers are dropped unless `passthrough-headers` is on. If the stream ends without `message_stop` or an error event, the proxy writes a 502 error event.

## Session affinity

- Session identity comes from `X-Claude-Code-Session-Id`, or `metadata.user_id.session_id`. Subagents are identified by `X-Claude-Code-Agent-Id` and `X-Claude-Code-Parent-Agent-Id` (`sdk/cliproxy/session/info.go:174`).
- `SessionAffinitySelector.Pick` (`sdk/cliproxy/auth/selector.go:1101`) keys bindings as `provider::session::model`. A bound, available account is reused. Otherwise the round-robin selector picks one and the session is bound to it.
- Subagents with no binding of their own inherit the parent's account when `session-affinity-subagents` is true.
- Bindings live only in memory (`sdk/cliproxy/auth/session_cache.go`): at most 65,536 entries, with a 1h TTL that resets on each use. A restart loses them, and so does any routing config change, because that rebuilds the selector (`sdk/cliproxy/service_config.go:264`).
- `session-affinity-rebind-on` decides what a failure does to a binding. `any` (our current setting, the default) moves the session to the failover account. `quota` keeps the binding through transient errors and moves it only on 429, 401-403, 404 or invalid_grant.
- Explicit session ids win over the prefix-matching (LCP) matcher, so the LCP code is not used for Claude Code traffic.

## Native passthrough versus cloaking

`helps.DetectClaudeCodeRequest` (`internal/runtime/executor/helps/claude_client_detection.go:172`) confirms a native Claude Code request when all of these hold:
- `X-App: cli`;
- a `claude-cli/2.1.x (external, <entrypoint>)` user agent with patch 280 or later;
- the `claude-code-20250219` beta;
- a valid `metadata.user_id`.

What the proxy does with a confirmed native request:
- It keeps the client's headers, betas, system prompt and cache breakpoints.
- It replaces the identity. `metadata.user_id` becomes `{device_id, account_uuid, session_id}`, where the device id is random per account and stored in the account's auth file, and the OAuth bearer token replaces the client's key.

Any other request is cloaked, to look like Claude Code:
- the Claude Code system prompt is injected and the caller's system prompt moves into the first user message;
- tools are renamed to `mcp__…` aliases;
- fixed user agent and stainless headers are used, with a synthesised beta list;
- the proxy places its own cache breakpoints with a 1h TTL;
- `context_management` and `diagnostics` are added to the body.

Most of this lives in `claude_executor_cloaking.go` and `claude_executor_request.go:1143-1608`.

## What the proxy changes on native requests

All of these are deterministic, so they don't break the cache prefix from one turn to the next on their own. Two exceptions are tracked in ticket `stable-thinking-config`.

- Sets `model` to the upstream name and normalises `tool_choice` and thinking config.
- Sanitises history (`claude_executor.go:87`): drops thinking blocks with empty or foreign signatures, strips non-Anthropic fields from `tool_use`, and rewrites non-portable tool ids.
- Refills empty `web_search_tool_result` content from a per-session store (`helps/claude_web_search_results.go:118`).
- Caps `cache_control` at 4 breakpoints; Claude Code sends no more than 4, so this does nothing for native traffic.
- Adds `thinking.block_binding.prefix_mismatch_behavior: drop_block` once the history holds a signed thinking block (`claude_executor_request.go:412`).
- Fills the `cch` hash in the billing block and signs the body (`claude_signing.go:298`). Real Claude Code sends the same block, and Anthropic strips it before caching.
- Appends the `oauth` and `extended-cache-ttl` betas.

## Errors and failover

- Upstream errors are classified in `claude_executor_request.go:842` and `sdk/cliproxy/auth/conductor_cooldown.go:889`.
  - An account-wide 429 (unified 5h or 7d window rejected) cools every model on the account until the reset.
  - 5xx errors cool the model for `retry-after`, or 1 minute without it.
  - 401-403 cool the account for 30 minutes.
- Transport errors such as DNS failures or refused connections are classed as transient and cool nothing. Within one request the conductor still skips accounts it has already tried, which is how a local network outage walks a session across accounts (ticket `failover-keeps-session-binding`).
- `request-retry: 3` allows up to three more rounds. A round restarts only for 403, 408, 429 and 500-504, and only if an account recovers within `max-retry-interval`.
- OAuth tokens refresh up to 4h before they expire. Refresh tokens are single-use.
