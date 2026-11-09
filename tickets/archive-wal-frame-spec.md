---
id: archive-wal-frame-spec
title: Specify the archive WAL segment and frame format shared with cliproxyapi-archiver
status: todo
priority: p1
dependencies: []
related: []
scopes: [archive]
shared_scopes: [project/tickets]
paths: []
tags: [archive, spec]
---
## Goal

A written, versioned specification of the write-ahead log that the proxy produces and `cliproxyapi-archiver` consumes, plus golden fixtures both implementations test against. Design record: fork/proposals/conversation-archive-2026-11-09.html.

## Gap

The Go writer and the Rust reader are built in different repos. Without a spec and shared fixtures, each side's assumptions about framing, encoding and crash recovery drift and the archive silently loses or misreads turns.

## Work

Write `docs/archive-wal-format.md` covering:

- Directory layout: one spool directory; segment files `seg-<seq:09d>.zst` with a strictly increasing u64 sequence; the directory listing is the only index.
- Segment: a zstd stream of frames; roll at 64 MB or 15 minutes; fsync before the segment is considered complete; a segment is complete when a newer one exists.
- Ring: at most 16 completed segments; the writer unlinks the lowest-numbered segment when it would exceed that; both writer and reader ignore `ENOENT`; one writer, one reader, no locks.
- Frame: `u32 length` + `u8 version` + payload; payload is a single JSON document (one turn) with fixed top-level keys: `request_id`, `attempt`, `ts`, `client` (user agent, headers after credential stripping), `session` (session_id, parent_session_id, agent_id), `route` (provider, model, upstream_model, account_id), `usage` (status, error, latency_ms, ttft_ms, token counts incl. cache 5m/1h), `request` (format, body as base64 or raw bytes decision), `response` (is_sse, body). Document the base64-vs-binary decision and the 32 MiB per-record cap.
- Crash recovery: a truncated final frame is skipped; an unterminated zstd stream is read up to the last complete block.
- Reader rules: oldest-first; newest-first when more than 12 completed segments exist; delete a segment only after its rows are confirmed uploaded.
- Versioning: how `version` bumps and how a reader treats an unknown version (skip frame, count it).

Produce golden fixtures under `internal/archive/testdata/`: a two-segment spool with a truncated tail frame and a mixed set of real (redacted) Claude, OpenAI chat, Responses and Gemini bodies. The archiver repo vendors the same fixtures.

## Acceptance

Spec reviewed by both implementations' authors; Go writer tests and Rust reader tests both pass against the identical fixture directory; a fixture with a truncated tail decodes to N-1 frames on both sides.
