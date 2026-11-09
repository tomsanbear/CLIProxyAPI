---
id: archive-wal-frame-spec
title: Specify the archive WAL segment and frame format shared with cliproxyapi-archiver
status: done
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

- Directory layout: one spool directory; segment files `seg-<seq:012d>.wal` with a strictly increasing u64 sequence, `.wal.open` while being written; `spool.lock` held by the single writer; the directory listing is the only index.
- Segment: a sequence of self-contained frames; roll at 64 MiB or 15 minutes; fsync then rename `.wal.open` to `.wal`; a crashed `.wal.open` is rolled by the next writer start.
- Ring: at most 16 completed segments; the writer unlinks the lowest-numbered segment when it would exceed that; both writer and reader ignore `ENOENT`; one writer, one reader.
- Frame: 20-byte header (`CPAW` magic, version, flags, payload_len, record_len, CRC-32C) + a per-frame zstd payload. The record is three length-prefixed binary sections: meta JSON, request body, response body (no base64). Meta carries request_id, attempt, timestamps, client headers after credential stripping, session, route, outcome, usage incl. cache 5m/1h, and body descriptors (format, kind, bytes, sha256, omitted). 64 MiB record cap with bodies omitted rather than the turn dropped.
- Damage handling: truncated tail ends the segment quietly; bad magic ends it as corrupt; unknown version, CRC mismatch and undecodable records are skipped and counted.
- Reader rules: oldest-first; newest-first when more than 12 completed segments exist; delete a segment only after its rows are confirmed uploaded; dedupe rows by `(request_id, attempt)` on replay.
- Versioning: header bytes 0-11 frozen; unknown versions skipped and counted; new meta keys never bump the version.

Produce golden fixtures under `internal/archive/testdata/wal/` with synthetic Claude, OpenAI chat, Responses and Gemini bodies, a failover pair, a truncated tail, an unknown-version frame, a bad-CRC frame and an in-progress segment, plus a `manifest.json` of expected counts and section hashes. The archiver repo vendors the same fixtures.

## Acceptance

Spec reviewed by the owner; the Go codec (`internal/archive`: frame encoder/decoder, segment reader, meta types) reproduces `manifest.json` from the fixtures and the Rust reader will be held to the same manifest; a fixture with a truncated tail decodes to N-1 frames on both sides.
