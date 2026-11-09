---
id: archive-capture-turns
title: "Capture each turn losslessly: inbound body, delivered response and usage metadata joined on request id"
status: todo
priority: p1
dependencies: [archive-wal-frame-spec]
related: []
scopes: [archive]
shared_scopes: [claude/executor, usage, project/tickets]
paths: []
tags: [archive]
---
## Goal

For every request the proxy serves, one complete turn record: the inbound body and headers as the agent sent them, the response bytes exactly as delivered to the agent (including synthesized error frames), and the usage metadata (session, account, model, tokens incl. cache fields, latency, TTFT, status), assembled in memory and handed to the WAL writer.

## Gap

The only existing full-body capture is `request-log`, which is text-formatted, forced off in commercial mode (`internal/runtime/executor/helps/logging_helpers.go:61`, `internal/api/server.go:181`) and lossy for streams: `internal/api/middleware/response_writer.go:119-123` drops chunks when its 100-slot channel fills. Usage records (`sdk/cliproxy/usage/manager.go:26-82`) carry the metadata but no bodies, and are emitted per upstream attempt.

## Work

- Reuse the inbound body already captured after auth by `CaptureAuthenticatedRequestBody` (`internal/api/middleware/request_logging.go:127`); never read the body a second time. Honour the 32 MiB cap.
- Tap the synchronous downstream writers (`ForwardStream.WriteChunk`/`WriteTerminalError`/`WriteDone` in `sdk/api/handlers/stream_forwarder.go:80-119`, `forwardClaudeStreamWithState` in `sdk/api/handlers/claude/code_handlers.go:475-517`, and the non-stream write paths) into a per-request buffer. No channel, no drops.
- Register a `usage.Plugin` (`RegisterNamedPlugin`, `manager.go:817`) that joins records to the body buffer on `TraceID` (the gin request id). Emit one turn per upstream attempt so retries and failovers are visible; the final attempt carries the delivered response.
- Complete the turn when both halves are present or when the request ends in error without a usage record (timeout-bounded join; count unmatched halves).
- Strip credentials: drop `Authorization`, `x-api-key`, `Cookie` and the proxy access key; store `caller_key_sha256`. Keep everything else.
- Works in commercial mode and independently of `request-log`.

## Acceptance

Integration test: a streamed Claude request with a mid-stream error produces one frame whose response bytes equal what the client received byte-for-byte, with usage fields populated. A request with a 429 failover produces two frames with attempts 1 and 2. Load test with slow client reads shows zero dropped chunks. No credential string appears in any frame.
