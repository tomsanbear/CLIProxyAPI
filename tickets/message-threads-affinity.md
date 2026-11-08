---
id: message-threads-affinity
title: Pin message-thread continuations to their creating account
status: todo
priority: p3
dependencies: []
related: [measure-cache-hit-rate]
scopes: [routing/affinity, clienterror]
shared_scopes: [claude/executor, project/tickets]
paths: []
tags: [cache-hit-rate, stickiness, threads]
---
## Goal

A request continuing a message thread always reaches the account that created the thread, and a thread-not-found 404 never cools an account.

## Gap

Thread state (`message-threads-2026-08-12`) lives server-side per organization, so a continuation only works on the creating account. The proxy has no thread-to-account pinning, so failover, restart or rebind gives a 404.

- The docs identify the 404 by `error.details.error_code: "thread_not_found"` and say the message text is deliberately vague. `IsClaudeThreadNotFound` (`internal/clienterror/client_error.go:153`) requires the text to contain "thread state" and "previous_message_id". An unmatched 404 cools the account and model for 12 hours (`conductor_cooldown.go:1110-1118`).
- count_tokens doesn't strip `thread` (`claude_executor_tokens.go:237-251`), which the docs say is a 400.
- `enforceCacheControlLimitDoc` reserves one breakpoint for a thread but doesn't count the top-level `cache_control` or the thread's stored breakpoints (`claude_executor_cloaking.go:3895`).
- The 2.1.319 binary contains the beta and a `thread: {type: create|continue}` body field; whether Claude Code sends them by default is unknown.

## Work

1. Match thread-not-found on `error.details.error_code` as well as the text check.
2. Pin a thread continuation to its creating account. Return a clear request-scoped error if that account is unavailable, so the client replays.
3. Strip `thread` on count_tokens.

## Acceptance

Tests show a cross-account thread 404 cooling nothing and reaching the client as a request error, and a continuation routed to its creating account.

## Refs

- https://platform.claude.com/docs/en/build-with-claude/message-threads
- Priority depends on the capture in `measure-cache-hit-rate`: if Claude Code sends `thread` on our traffic, raise this to p1.
