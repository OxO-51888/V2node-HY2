# Dedicated HY2 Panel Contract

All requests use `/api/v2/server/hy2/` and these headers:

- `Authorization: Bearer <site server token>`
- `X-Node-ID: <positive node ID>`
- `X-V2node-Contract: hy2-1`

Public panel origins require HTTPS. Numeric loopback HTTP is permitted for local
integration testing. Credentials never appear in the query string, and redirects
are not followed. Successful responses must return the same contract header.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | config | Validated HY2 configuration, conditional ETag response |
| GET | users | Full authorized user snapshot, conditional ETag response |
| GET | state | Next traffic report sequence |
| POST | traffic | Transactional traffic accounting and revoked user IDs |
| GET | alive | Cross-node, deduplicated online IP counts |
| POST | alive | Full online-device replacement and idle heartbeat |

Configuration and user ETags are committed only after successful application.
An empty user array revokes all users; 304 leaves users unchanged. Errors and
invalid bodies do not replace a valid snapshot.

## Accounting

A report contains `sequence` and up to 500 `rows`, each with integer `uid`, `u`,
and `d`. The response contains `accepted`, matching `sequence`, the SHA256 digest
of the exact request bytes, and `revoked` user IDs. The panel commits user
counters, statistics, and its per-node cursor in one database transaction.
Replaying the last sequence with the same bytes does not charge twice. A changed
payload or out-of-order sequence is rejected.

A rejected traffic POST returns HTTP 409 with `error=traffic_sequence_conflict`,
the rejected `request_sequence` and `request_digest`, and the panel's current
`sequence`, `digest` and `next`. Contract headers and JSON content type are still
required. Only an exact sequence collision with a different accepted digest proves
that the immutable pending batch was not accepted at that sequence. The backend
can durably rebase that batch to `next` without changing its rows, then retry once
per reporting round. The recovered/live counter distinction must survive rebasing.
If the cursor is further ahead, the response is malformed, or the rejected digest
does not match the pending bytes, the backend retains the batch and reports the
conflict instead of guessing whether to discard or charge it. Rebase persistence
and acknowledgement failures follow the same durable replay rules as normal reports.

Deploy the paired panel response before the backend recovery change. Legacy 409
responses without proof never authorize rebasing. State and other node responses
are private/no-store and vary by node identity; backend requests also bypass caches.

The backend stores one cursor and at most one pending report per node under
`StateDirectory` (default `/var/lib/v2node-hy2`). The service account needs write
access. Preserve this directory during updates; do not share one node identity
between multiple backend processes. A pending report is written and synchronized
before sending. Acknowledgement loss retries the exact report. Only a verified
acknowledgement permits in-memory counters to be committed. Corrupt state fails
closed rather than silently discarding unconfirmed usage.

Revocation stops authorization immediately but retains the user's tail counters
until acknowledged, including amounts below the usual report threshold. Shutdown
and reload first cancel and join controller tasks, then quiesce forwarding and
persist the remaining counters. The same journal may contain a bounded shutdown
buffer (200,000 rows, 16 MiB maximum) in addition to the pending batch. Restart
replays these bytes before accepting users, without subtracting them from a new
runtime. A failed durable drain must not be reported as successful shutdown.

Traffic rounds have a 12-second request budget and a 200-batch cap, rotating the
starting UID to avoid starving users beyond a slow batch. Heartbeats run separately
and never wait for the traffic outbox. In-flight user snapshots are fenced by a
generation counter so an acknowledgement cannot be undone by an older user list.

Exhausted users are revoked after the accounting acknowledgement. This is not a
zero-overrun reservation system: unreported traffic on this or other nodes remains
subject to the reporting interval. In-memory bytes not yet captured in the durable
pending report are not guaranteed to survive an abrupt process or machine crash.

## Release Requirements

The paired panel migrations create `v2_hy2_report_cursor`, `v2_hy2_online` and
`v2_hy2_user_revision`. MySQL migration credentials need TRIGGER privileges.
Database triggers invalidate user snapshots on raw SQL and model writes affecting
authorization; traffic updates that remain within quota do not rebuild the list.
Time-based account expiry independently bounds each cached snapshot. Online rows
store their observation time and expire after `max(120, 3 * push_interval + 30)`
seconds. Rule checks share one service instance and query online state in batches.
The old V1 server routes/controllers and generic V2 server controller are removed,
not retained as compatibility fallbacks. Existing administrative and client
subscription interfaces remain separate from this backend-only contract.

Do not push or deploy an unverified candidate. Verify Go unit/race tests, isolated
PHP/MySQL rollback and concurrent accounting tests, and Go-to-PHP HTTP integration.
Also run real HY2 processes and forwarding while injecting slow panel responses,
quota revocation, watched-config reload and SIGTERM. Test only isolated loopback
listeners and a private database, never production customer traffic or billing.
High-iteration logic stress tests do not represent concurrent real users or an
equivalent number of network requests.
