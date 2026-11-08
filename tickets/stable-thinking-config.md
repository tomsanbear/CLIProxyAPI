---
id: stable-thinking-config
title: Keep thinking settings and history unchanged across turns
status: todo
priority: p2
dependencies: [measure-cache-hit-rate]
related: []
scopes: [claude/executor, claude/helpers]
shared_scopes: [project/tickets]
paths: []
tags: [cache-hit-rate]
---
## Goal

Two consecutive native Claude Code requests from one session differ only in what the client appended. The proxy adds nothing that changes between turns.

## Gap

- `applyClaudePreservedThinkingBindingDoc` (`claude_executor_request.go:412-433`, called on every request at `claude_executor_execute.go:246` and `claude_executor_stream.go:240`) adds `thinking.block_binding.prefix_mismatch_behavior: drop_block`, plus `thinking: {type: adaptive}` when `thinking` is absent. It only does so once history holds a signed thinking block, so turn 1 goes out without it and turn 2 onward with it. The caching docs list a `thinking` change as a messages-cache invalidator, which probably costs one miss per session and per subagent at turn 2.
- `RestoreClaudeWebSearchResultsContextDoc` (`helps/claude_web_search_results.go:116-118`) refills empty `web_search_tool_result` content from a per-session store, or drops `web_search_result_location` citations when nothing is stored. Either is an edit to an earlier turn.
- `drop_block` turns prefix mismatches into silent drops, so these edits never surface as errors.

## Work

1. First use the capture from `measure-cache-hit-rate`: if Claude Code already sends `block_binding`, item 2 doesn't apply to native traffic.
2. On preserved-thinking models, apply the same thinking settings from turn 1 whenever the thinking type accepts a binding (the docs accept it with or without signed blocks), so `thinking` never changes mid-session.
3. Leave history unchanged when the web-search store has no copy, and confirm a refill reproduces the original bytes exactly.

## Acceptance

- A capture of two consecutive native requests shows identical `thinking` objects.
- `measure-cache-hit-rate` shows no cold write at turn 2.

## Refs

- https://platform.claude.com/docs/en/build-with-claude/preserved-thinking
- Open question: whether adding `thinking: adaptive` where Claude Code omitted it is itself cache-relevant. The docs treat the two as equivalent in behaviour, but say nothing about the cache key.
