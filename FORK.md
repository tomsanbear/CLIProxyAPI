# Fork tracker

This is the entry point for work on the `local/maintained` fork of CLIProxyAPI. Work items are [ticketsplease](https://github.com/moderately-ai/ticketsplease) tickets in `tickets/`; this file only points at them and at the background they share.

Our setup: Claude Code 2.1.319 talks to the proxy at `127.0.0.1:8317`, which routes to 7 Claude OAuth accounts with round-robin selection and session affinity. The objective is a high prompt-cache hit rate without making the proxy visible to either side.

## Working with the tickets

```sh
tkt rollup --tag cache-hit-rate   # where the initiative stands: counts, ready set, blocked set
tkt tracks                        # what can run in parallel now
tkt show <id>                     # one ticket with its comments
tkt claim <id> --as <worker>      # take a ticket; branch as tkt/<id>
tkt guard tkt/<id>                # before merging: does the diff stay in the ticket's scopes?
```

- Scopes in `ticketsplease.toml` name the code areas (`routing/affinity`, `routing/conductor`, `claude/executor`, `claude/helpers`, `usage`, `clienterror`, `config`, `fork/docs`). Every ticket declares the scopes it edits.
- `default_base` is `local/maintained`, not `main`: `main` mirrors upstream.
- Record findings that change a ticket as a comment (`tkt comment add`) or by editing its body; keep this file short.

## Current goal

Not chosen yet. The owner picks it from `tkt rollup --tag cache-hit-rate`.

## Background

- [How the proxy works](fork/context/how-it-works.md): request path, session affinity, native passthrough versus cloaking, what the proxy changes in a request.
- [Log evidence, 2026-11-08](fork/context/log-evidence-2026-11-08.md): affinity outcomes and failover causes measured from the live proxy logs.
- [Docs audit, 2026-11-08](fork/context/docs-audit-2026-11-08.md): the proxy checked against the current Anthropic API docs and the Claude Code 2.1.319 binary.
- [Conversation archive design, 2026-11-09](fork/proposals/conversation-archive-2026-11-09.html): every turn through the proxy recorded to a ring-buffered WAL, archived as Vortex in R2 by the Rust `cliproxyapi-archiver`, queried with DataFusion. Tickets tagged `archive`.

Put background that several tickets share in `fork/context/`, and date files that record a point-in-time observation.
