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

- Config: new top-level `archive` section following the `pprof` template (`internal/config/config_types.go:414`, field in `config.go:63`, defaults `config_load.go:205`, v8 path map `config_v8.go:116`, `config.example.yaml`): `enabled`, `spool-dir`, `segment-max-bytes` (64 MiB), `segment-max-age` (15m), `ring-segments` (16), `r2.endpoint`, `r2.bucket`, `r2.prefix`, `r2.access-key-id`, `r2.secret-access-key`, `r2.sse-c-key` (base64 AES-256). The R2 and SSE-C fields are consumed only by the archiver, which reads the same file; the proxy validates shape only.
- Writer: `internal/archive/` package; one open segment, zstd encoder, frame append with the spec's header, roll on size or age, fsync on roll, sequence from the highest existing segment + 1 at start; ring enforcement on roll (unlink lowest; ignore `ENOENT`); counters for frames written, bytes, segments dropped, frames dropped (write error or cap), ring depth.
- Runtime component in `sdk/cliproxy/archive_*.go` modelled on `sdk/cliproxy/pprof_server.go`: `ApplyContext(ctx, cfg)` starts/stops/reconfigures; started next to `usage.StartDefault` in `service_lifecycle.go:59`; flushed and closed in `Shutdown` before `usage.StopDefault` (`:394`); hot-reload via `applyConfigRuntime` (`service_config.go:204`).
- Never block: a write error or cap drops the frame, increments a counter and logs once per minute.
- Management API + panel: settings for the section (secrets masked on read), and a status card with ring depth, segments/frames dropped, last frame time, last upload time (written by the archiver into `<spool-dir>/archiver-status.json`).

## Acceptance

Unit tests with a mock clock: roll on size, roll on age, ring eviction order, sequence continuity across restart, truncated-tail fixture from `archive-wal-frame-spec` decodes as specified by the Go reader used in tests. Fault test: a read-only spool directory produces dropped-frame counts and the request still succeeds. Panel shows the status card; secrets never appear in `GET` responses.
