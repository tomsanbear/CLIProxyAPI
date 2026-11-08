# Log evidence, 2026-11-08

Taken from the live proxy logs in `~/.local/share/cliproxyapi/logs/` (`main*.log`, 2026-10-31 to 2026-11-08). Account names are left out on purpose.

## Session-affinity outcomes

| Selector log line | Count |
|---|---:|
| `cache hit` (bound account reused) | 138,138 |
| `cache miss, new binding` | 1,300 |
| `fallback cache hit` (subagent inherits parent) | 744 |
| `cache hit but auth unavailable, reselected` | 474 |

Each reselection moves a live session to a different account, which means a full prompt-cache miss on the next request.

## What caused the reselections

Reselections cluster in a few hours, and those hours match local network failures (`no such host`, `network is unreachable`, connect timeouts) almost exactly:

| Hour | Network failures | Reselections |
|---|---:|---:|
| 2026-10-31 14:00 | 112 | 84 |
| 2026-11-02 16:00 | 336 | 252 |
| 2026-11-05 15:00 | 72 | 36 |

Over the whole period, failure reasons were 552 DNS lookups, about 36 connect timeouts or unreachable networks, a handful of broken pipes, and about 30 genuine 429 `rate_limit_error` responses.

Tracing one request through the 2026-11-02 outage (request id `5bb83496`) shows the mechanism. One session went through every available account in about one second, each attempt failing on DNS, and ended up bound to whichever account it was on when the network came back. The cause is in ticket `failover-keeps-session-binding`.

## Traffic mix

Requests seen by the selector in the current log: Opus 5.5 about 13,900, Fable 5.1 about 1,400, Sonnet 5.5 about 1,300, Sonnet 5 about 190.

## Gaps in what the logs can show

`usage-statistics-enabled` and `request-log` are both off, and nothing logs `cache_read_input_tokens` or `cache_creation_input_tokens`. The logs show when sessions move, but not the hit rate itself. Ticket `measure-cache-hit-rate` covers this.
