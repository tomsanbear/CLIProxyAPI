# Fork tracker

This file tracks work on the `local/maintained` fork of CLIProxyAPI: what we know about how the proxy behaves for our setup, and which fixes are open. It is an index. Detail lives in `fork/`, so this file stays short.

Our setup: Claude Code 2.1.319 talks to the proxy at `127.0.0.1:8317`, which routes to 7 Claude OAuth accounts with round-robin selection and session affinity. The main objective is a high prompt-cache hit rate without making the proxy visible to either side.

## Rules for this tracker

- Keep this file under about 80 lines. Add a row to the table below and put everything else in a task file.
- One task per file in `fork/tasks/`, named `T<nn>-<slug>.md`, using the layout of the existing files: status, problem, evidence, proposed fix, done-when, open questions.
- Status values: `open`, `in progress`, `blocked`, `done`. Update the task file and its row together.
- When a task is done, move its file to `fork/tasks/done/` and move its row to the Done list below as a single line.
- Background that several tasks share goes in `fork/context/`. Date files that record a point-in-time observation.
- Code references are `path:line` against the commit named in each file; re-check them before editing.

## Current goal

Not chosen yet. The owner picks the first goal from the open tasks.

## Background

- [How the proxy works](fork/context/how-it-works.md): request path, session affinity, native passthrough versus cloaking, what the proxy changes in a request.
- [Log evidence, 2026-11-08](fork/context/log-evidence-2026-11-08.md): affinity outcomes and failover causes measured from the live proxy logs.
- [Docs audit, 2026-11-08](fork/context/docs-audit-2026-11-08.md): the proxy checked against the current Anthropic API docs and the Claude Code 2.1.319 binary.

## Open tasks

Priority reflects the effect on our own Claude Code traffic: P1 costs cache or reliability today, P2 is a likely or latent cost, P3 affects other clients or edge cases.

| ID | Task | Priority | Status |
|---|---|---|---|
| [T01](fork/tasks/T01-client-1h-cache-ttl.md) | Turn on the 1-hour cache TTL in Claude Code | P1 | open |
| [T02](fork/tasks/T02-failover-rebinding.md) | Stop transient failures from moving sessions to another account | P1 | open |
| [T03](fork/tasks/T03-cache-observability.md) | Measure cache hit rate per account and session | P1 | open |
| [T04](fork/tasks/T04-overload-and-stream-errors.md) | Handle 529 and mid-stream errors correctly | P1 | open |
| [T05](fork/tasks/T05-stable-thinking-config.md) | Keep thinking settings and history unchanged across turns | P2 | open |
| [T06](fork/tasks/T06-restart-proof-affinity.md) | Keep session bindings across restarts and config reloads | P2 | open |
| [T07](fork/tasks/T07-native-detection.md) | Make native Claude Code detection robust | P2 | open |
| [T08](fork/tasks/T08-quota-aware-binding.md) | Bind new sessions to accounts with quota headroom | P3 | open |
| [T09](fork/tasks/T09-message-threads.md) | Support message threads across accounts | P3 | open |
| [T10](fork/tasks/T10-cloaked-path-gaps.md) | Fix API gaps on the cloaked and translated paths | P3 | open |

## Done

None yet.
