# Docs audit, 2026-11-08

The proxy (commit `511ede8b`) checked against the current Anthropic docs and the Claude Code 2.1.319 binary. Findings that need work are tasks; this file records the sources, the facts the tasks rely on, and what remains unconfirmed.

## Sources

- Anthropic API docs on platform.claude.com, fetched 2026-11-08:
  - [prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
  - [cache diagnostics](https://platform.claude.com/docs/en/build-with-claude/cache-diagnostics)
  - [preserved thinking](https://platform.claude.com/docs/en/build-with-claude/preserved-thinking)
  - [errors](https://platform.claude.com/docs/en/api/errors)
  - [rate limits](https://platform.claude.com/docs/en/api/rate-limits)
  - [message threads](https://platform.claude.com/docs/en/build-with-claude/message-threads)
  - [context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing)
  - [mid-conversation system messages](https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages)
  - [streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)
  - [versioning](https://platform.claude.com/docs/en/api/versioning)
  - [release notes](https://platform.claude.com/docs/en/release-notes/overview)
- Some of these pages were longer than the fetch limit and were read only in part: prompt caching, preserved thinking, messages create, context editing, and the release notes before July 2026.
- The bundled `claude-api` skill reference, dated 2026-10-06.
- Claude Code docs: [LLM gateway](https://code.claude.com/docs/en/llm-gateway) and the [changelog](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md) for 2.1.309 to 2.1.319 only.
- The local Claude Code binary, `~/.local/share/claude/versions/2.1.319`, searched with `strings`. It was never run against the network.

## Facts the tasks depend on

- **Caching**
  - Caches are isolated per workspace or organization, so each OAuth account has its own cache and an account switch is a full miss.
  - TTL is counted from the start of the request that writes or reads the entry.
  - A change to `thinking` or `effort` invalidates the messages cache.
  - Cache diagnostics no longer need a beta header. A request carries `diagnostics.previous_message_id`, and the response reports where the prompt diverged.
- **Claude Code TTL.** The binary says the TTL is "5 minutes unless ENABLE_PROMPT_CACHING_1H=1". A logged-in subscriber or an allowlisted account can also get 1h. We authenticate with `apiKeyHelper`, so we get 5 minutes.
- **Preserved thinking.** Opus 5.5, Fable 5.1, Sonnet 5.5 and Haiku 5.5 bind thinking blocks to the conversation prefix. Sonnet 5.5 and Haiku 5.5 blocks are also bound to the producing account, unless accounts are linked. Blocks replayed from another account are dropped silently, and the response lists them in `input_transformations`.
- **Errors**
  - 529 `overloaded_error`, 500 `api_error` and 504 `timeout_error` are retryable, and clients should honour `retry-after`.
  - Errors can also arrive as an SSE `error` event after HTTP 200.
  - The `anthropic-ratelimit-unified-*` headers the proxy parses are not in the API docs, so that parsing is reverse-engineered.
- **Threads.** Thread state is held server-side per organization, workspace and user. An unknown thread returns 404 with `error.details.error_code: "thread_not_found"`. The 2.1.319 binary contains the `message-threads-2026-08-12` beta and a `thread` body field.

## Agent claims rejected on checking

- "Haiku 5.5 helper calls fail because they send `temperature: 1`." Haiku 5.5 rejects only non-default sampling values, and 1 is the default.
- "The `server-side-fallback-2026-06-01` beta is stale." It is the correct pairing for the array form of `fallbacks` that the proxy injects. Only the `"default"` form needs `-07-01` (T10).

## Still unconfirmed

- Whether Claude Code already sends `thinking.block_binding`. If it does, the turn-2 change in T05 doesn't happen.
- Whether our 7 accounts are linked for Sonnet 5.5 thinking.
- Whether Claude Code sends `thread` by default, and the exact wording of the upstream thread-not-found error (T09).
- Whether `fallback-credit-2026-06-01` is still accepted; the docs name `fallback-credit-2026-07-01` (T10).
- Whether a Claude 413 is treated as request-scoped or cools the account.
- Whether `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS` would remove the `claude-code-20250219` beta. Native detection depends on that beta, and our environment doesn't set the variable.
