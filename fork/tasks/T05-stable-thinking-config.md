# T05: Keep thinking settings and history unchanged across turns

Status: open · Priority: P2 · Area: `internal/runtime/executor/claude_executor_request.go`, `helps/claude_web_search_results.go`

## Problem

On native Claude Code requests the proxy makes two changes that can differ from one turn to the next:

- **Thinking binding.** `applyClaudePreservedThinkingBindingDoc` adds `thinking.block_binding.prefix_mismatch_behavior: drop_block`, and `thinking: {type: adaptive}` if `thinking` is absent. It does this only once the history holds a signed thinking block, so turn 1 goes out without it and turn 2 onward with it. The caching docs list a change to `thinking` as a messages-cache invalidator, so this probably costs one miss per session and per subagent at turn 2. The docs don't say how large the miss is.
- **Web search refill.** `RestoreClaudeWebSearchResultsContextDoc` refills empty `web_search_tool_result` content from a per-session store. If the store has no entry, it drops `web_search_result_location` citations. Either way it is an edit to an earlier turn, which the preserved-thinking check treats as invalid.

Because `drop_block` turns prefix mismatches into silent drops, these edits never show up as errors.

## Evidence

- The binding runs for every request, native included (`claude_executor_execute.go:246`, `claude_executor_stream.go:240`). The logic is at `claude_executor_request.go:412-433`. It is gated on `claudeDocHasSignedThinkingBlock`.
- The refill is at `helps/claude_web_search_results.go:116-118`, called from `claude_executor_execute.go:98` and `claude_executor_stream.go:103`.
- The preserved-thinking docs say the binding is accepted whenever thinking is adaptive or enabled, signed blocks or not.

## Proposed fix

1. On preserved-thinking models, apply the same thinking settings from turn 1 whenever the thinking type accepts a binding. Drop the signed-block gate so `thinking` doesn't change partway through a session.
2. Change the web-search refill to leave history unchanged when no stored copy exists, and confirm a refill reproduces the original bytes exactly.
3. Log `input_transformations` ([T03](T03-cache-observability.md)) so drops become visible.

## Done when

- A capture of two consecutive native requests shows identical `thinking` objects.
- [T03](T03-cache-observability.md) shows no cold write at turn 2.

## Open questions

- Whether Claude Code 2.1.319 already sends `block_binding`. If it does, part 1 does nothing for native traffic. Check with a request capture before changing code.
- Whether adding `thinking: adaptive` where Claude Code omitted it is itself a cache-relevant change. The docs treat omitting it and sending adaptive as equivalent for behaviour, but say nothing about the cache key.
