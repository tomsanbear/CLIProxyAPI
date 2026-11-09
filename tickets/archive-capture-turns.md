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

- The request-log body capture cannot be reused: `RequestLoggingMiddleware` is installed only when commercial mode is off (`internal/api/server.go:182`) and `shouldCaptureRequestBody` returns false unless request-log is enabled (`request_logging.go:398`). Add an archive middleware, registered after `responseCompressionMiddleware` (`server.go:202`) and active whenever `archive.enabled` is true, that after authentication reads the inbound body once into memory (decoding `Content-Encoding` the same way `request_logging.go:453-498` does, 32 MiB cap) and resets `c.Request.Body`.
- The same middleware wraps `gin.ResponseWriter` so every write path is seen: the direct first-chunk write (`sdk/api/handlers/claude/code_handlers.go:338`), `ForwardStream` chunks and keep-alives (`stream_forwarder.go:155`), `c.Writer.Write` and `c.JSON` on non-stream and error paths (`code_handlers.go:135,212`, `handlers_errors.go:181,200`). Because it sits after the compression middleware it records pre-compression bytes. Per-request in-memory buffer; no channel, no drops.
- Register a `usage.Plugin` (`RegisterNamedPlugin`, `manager.go:817`) that joins records to the captured request on `TraceID` (the gin request id). Usage records carry no attempt number and arrive asynchronously after the response: number them per `TraceID` in arrival order (the manager dispatches from one queue in order, `manager.go:786`), emit one turn per attempt with `outcome.upstream_status` from `Fail.StatusCode` or the response status, and attach the delivered response bytes to the final attempt.
- Complete the request when the response has finished and its usage record has arrived, or 5 s after the response finished with no record (pre-executor failures such as `code_handlers.go:77`), emitting a single `attempt: 1` record with zero usage. Count both outcomes.
- Strip credentials: drop `Authorization`, `x-api-key`, `Cookie` and the proxy access key; store `caller_key_sha256`. Keep everything else.
- Works in commercial mode and independently of `request-log`.

## Acceptance

Integration test with commercial mode on and request-log off: a streamed Claude request with a mid-stream error produces one frame whose response bytes equal what the client received byte-for-byte (keep-alive comments included), with usage fields populated. A request with a 429 failover produces two frames with attempts 1 and 2 and `upstream_status` 429 then 200. A 400 from request validation produces one frame with zero usage after the grace period. Load test with slow client reads shows zero dropped bytes. No credential string appears in any frame.
