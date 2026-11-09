---
id: archive-wal-writer-config
title: Ring-buffered WAL writer, `archive` config section and management-panel status
status: todo
priority: p1
dependencies: [archive-wal-frame-spec]
related: []
scopes: [archive, config]
shared_scopes: [fork/docs, project/tickets]
paths: []
tags: [archive]
---
## Goal

The proxy writes turn frames to the spool exactly as the spec says, bounded by a 1 GB ring, never blocking a request, with its configuration editable in the management panel and its health visible there.

## Gap

No spool, batcher or disk-backed queue exists in the proxy (`internal/logging/home_app_log_forwarder.go` is a bounded in-memory queue; `request_logger_streaming.go` spools to temp files but assembles text). No config section describes the archive.

## Work

- Config: new top-level `archive` section exactly as spec section 8 defines it (keys, duration grammar, byte sizes), following the `pprof` template for plumbing (`internal/config/config_types.go:414`, field in `config.go:63`, defaults `config_load.go:205`, `config.example.yaml`). It stays top-level in the v8 layout (no entry under `observability`). The `r2` sub-section is consumed only by the archiver; the proxy validates shape only and never logs it.
- Writer: `internal/archive/` package on top of the existing codec; `spool.lock` via flock at start (refuse and disable archiving if held); one `.wal.open` segment, frame append, roll on size or age with fsync + rename, roll any leftover `.wal.open` at start, sequence from the highest existing segment + 1; ring enforcement on roll (unlink lowest; ignore `ENOENT`); counters for frames written, bytes, segments dropped, frames dropped (write error or cap), ring depth, logged on change and once a minute.
- Runtime component in `sdk/cliproxy/archive_*.go`: started next to `usage.StartDefault` in `service_lifecycle.go:59` when `archive.enabled`, flushed and closed in `Shutdown` before `usage.StopDefault` (`:394`). Hot reload of the section is deferred; a config change takes effect on restart.
- Never block: a write error or cap drops the frame, increments a counter and logs once per minute.
- Management-panel settings page and status card are deferred to a later ticket; the MVP exposes the writer counters through the existing log and the archiver's `status` subcommand.

## Acceptance

Unit tests with a mock clock: roll on size, roll on age, ring eviction order, sequence continuity across restart including a leftover `.wal.open`, a second writer refused by `spool.lock`, and every written segment decodes with `ReadSegment` to the records written. Fault test: a read-only spool directory produces dropped-frame counts and the request still succeeds. The `r2` secrets never appear in logs.
