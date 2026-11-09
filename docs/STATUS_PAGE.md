# Public status page

The API process samples its own health and serves a status page that does not
depend on the admin application. An admin frontend outage leaves `/status`
available. The page is a customer view: internal workers can change the banner
without being named.

Sampling starts during route setup and then runs every 30 seconds until the
process shuts down. Each check has a two-second timeout. A panicked check is
recorded as an outage with the summary `Check failed`.

## Endpoints

| Method and path | Who | Body |
| --- | --- | --- |
| `GET /status` | Anyone | HTML. Refreshes every 30 seconds. |
| `GET /api/v1/status` | Anyone | The same public snapshot as JSON. |
| `GET /health`, `GET /api/v1/health` | Anyone | Liveness only. Does not touch the database or the status history. |
| `GET /ready`, `GET /api/v1/ready` | Anyone | Database ping, two-second timeout. Includes `version` and process `uptime_seconds`. |
| `GET /api/v1/admin/status` | Active admin | Full probe report: identifiers, latency, and recent checks. |

`/status`, `/health`, and `/ready` are the only routes outside `/api/v1`.
Support staff receive 403 on the admin route. A missing monitor returns 503
`{"error":"Status is unavailable"}`.

Public responses send `Cache-Control: no-store`, `X-Frame-Options: DENY`,
`Referrer-Policy: no-referrer`, and a Content-Security-Policy that blocks
framing, images, and form posts. Do not cache the page at the proxy.

```json
{
  "generated_at": "2026-11-09T13:00:00Z",
  "overall": "operational",
  "summary": "All systems operational",
  "range_days": 90,
  "components": [
    {
      "name": "Rida API",
      "group": "Platform",
      "state": "operational",
      "summary": "Operating normally",
      "uptime_percent": 100,
      "days": [{"date": "2026-11-09", "state": "operational"}]
    }
  ]
}
```

`uptime_percent` is JSON `null` until the component has at least one
operational, degraded, or outage sample. `days` always contains 90 entries for
a component that is present.

## What visitors see

Four rows, in this order:

| Public name | Group | Source probe |
| --- | --- | --- |
| Rida API | Platform | `api` |
| Database | Platform | `database` |
| Notifications | Platform | `notifications` |
| Ride matching | Operations | `ride-dispatch` |

The public summary is one of `Operating normally`, `Performance is reduced`,
`Unavailable`, or `No data yet`. Probe text such as `Database responses are
slow` or `Ride dispatch tick failed` is not shown. A public row whose internal
state is `maintenance` is shown as `No data yet` and does not move the banner.
None of the four rows above use maintenance today.

The banner uses the current sample only:

| Condition | `overall` | `summary` |
| --- | --- | --- |
| Any counted component is `outage` | `outage` | Service disruption |
| Otherwise any counted component is `degraded` | `degraded` | Some systems are degraded |
| Otherwise | `operational` | All systems operational |

## What stays off the public page

The HTML page and `/api/v1/status` omit component identifiers, descriptions,
latency, the 24-hour latency series, check logs, process `uptime_seconds`, and
`version`.

These probes still exist and still move the public banner when their current
state is `degraded` or `outage`. They have no public row and no public day bar,
so a recovered incident disappears from the customer page. Read
`/api/v1/admin/status` to see which one failed.

| ID | Admin name | When it runs |
| --- | --- | --- |
| `document-expiry` | Document checks | Always, in the API process |
| `operations` | Payments, safety and fraud | Only when `RUN_BACKGROUND_JOBS=true` |
| `data-requests` | Data requests | Only when `RUN_BACKGROUND_JOBS=true` |

When `RUN_BACKGROUND_JOBS` is false, payments and data-request probes stay in
`maintenance` (`This process is not running background jobs`). Maintenance does
not change the public banner. `cmd/process-payments`, `cmd/process-fraud`, and
`cmd/process-data-requests` do not report heartbeats, so a healthy external
scheduler is invisible here, and a stuck scheduler does not mark the page down.

A hidden outage is enough on its own. If data requests are stale while the four
public rows are healthy, visitors still see **Service disruption**.

## How a sample is judged

### Rida API

The process records finished HTTP responses, excluding `/health`, `/ready`,
`/status`, and their `/api/v1` aliases, so a database outage is not also counted
as an API failure and the status page cannot mark itself down. Status codes
below 100 are ignored. A response is a failure only at 500 or above; 4xx does
not count. The window is the last five minutes, capped at 4,000 observations.

Under 20 counted responses, the API stays operational. At 20 or more, a failure
ratio of at least 5% is degraded (`Some requests are failing`) and at least 20%
is an outage (`Many requests are failing`). A quiet process can therefore stay
green through a handful of 500s. Latency on the admin report is the average of
that same window.

### Database

The probe pings the primary database. An error, or a check cancelled by the
two-second timeout, is an outage (`Database did not respond`). A successful ping
slower than 800ms is degraded (`Database responses are slow`).

### Notifications

With `PUSH_PROVIDER=fcm` and a configured sender, this row is the
`push-deliveries` worker (stale after 60 seconds, dead after 3 minutes). The
worker ticks about every 10 seconds. Push disabled is a standing operational
row: the inbox is served with the API. The admin summary says push is off; the
public row says `Operating normally`.

### Workers

Jobs call `statuspage.Succeed` or `statuspage.Fail` with a fixed phrase.
`Fail` must not receive a driver error, SQL text, or request data. Phrases are
collapsed to single spaces and cut at 180 characters. Shutdown does not record
a failure when the worker context is already cancelled.

| Worker | Tick | Stale (degraded) | Dead (outage) |
| --- | --- | --- | --- |
| `ride-dispatch` | about 5 seconds | 45 seconds | 2 minutes |
| `push-deliveries` | about 10 seconds | 60 seconds | 3 minutes |
| `operations` | about 10 seconds | 2 minutes | 5 minutes |
| `data-requests` | about 5 minutes | 12 minutes | 20 minutes |
| `document-expiry` | startup, then midnight EAT | 26 hours | 50 hours |

Until the first heartbeat, the probe is operational for 90 seconds after
process start (`Worker is starting`), degraded until five minutes (`Worker has
not reported`), then an outage. That startup grace is separate from the stale
and dead windows, which apply only after a heartbeat exists. Document expiry
records its heartbeat only after the sweep returns, so a first sweep longer
than 90 seconds marks that hidden row degraded and can change the public banner
before the sweep finishes.

After a heartbeat, age past the dead window is an outage (`Worker heartbeat is
stale`). Age past the stale window is degraded (`Worker heartbeat is delayed`).
A recent `Fail` inside the stale window stays degraded and keeps that call's
fixed summary on the admin report.

## Uptime history

Days are calendar dates in a fixed UTC+3 zone (EAT), not the host zone. The
window is 90 days including today. A day's bar is the worst sample that day:
outage, then degraded, then operational, then maintenance, then no data. One
outage sample paints the whole day red even if every other sample was healthy.

The percentage is `(operational + 0.5 × degraded) / (operational + degraded + outage) × 100`,
rounded to two decimals. Maintenance samples are excluded. No counted samples
leaves the percentage unset (`Collecting uptime` on the page).

History is `history.json` under `STATUS_DIR`. When `STATUS_DIR` is unset, the
process uses `uploads/private/status`, which is on the Docker `uploads_data`
volume (`/app/uploads`). The directory is created mode `0750` and the file is
written mode `0640` via a temporary file in the same directory. An unwritable
directory or a failed save keeps history in memory and logs
`status history directory is not writable` or `status history was not saved`
once. A file that is not valid JSON is ignored (`status history file was
ignored`).

The file stores daily counts and up to 80 check-log entries. Logs are also
dropped after seven days. A log line is written on a state change, when an
operational component repeats after 15 minutes, or when a degraded or outage
component repeats after one minute. The 24-hour latency series (48 buckets of
30 minutes) stays in memory and is not in the file.

Restart keeps the on-disk counts and logs. Heartbeats, the API error window,
and latency buckets start empty, so workers show as starting until they report
again. Replacing the API container keeps history only when the file is on a
retained volume. Do not publish `STATUS_DIR`. One API process should write the
file; two writers on the same path can drop each other's samples.

## Admin report

`GET /api/v1/admin/status` returns every probe, including maintenance rows, plus
`version` (currently `1.0.0`), process `uptime_seconds`, `latency_ms`,
`latency_label`, the in-memory latency series, and `logs` newest first. Logs
contain the fixed summaries and component identifiers. They do not contain
connection strings, hosts, or request bodies. Treat the response as staff-only.

## Operator checks

1. Publish `GET /status` on the HTTPS proxy if customers should open it. The
   API listens on loopback by default; proxying only `/api` hides the HTML page.
   `/api/v1/status` is the machine-readable equivalent.
2. Confirm `history.json` survives a deploy: `STATUS_DIR` must be on
   `uploads_data` or another volume the entrypoint does not replace.
3. After a public **Service disruption** with healthy API, database,
   notifications, and ride matching rows, open the admin report. The cause is
   a hidden worker.
4. With `RUN_BACKGROUND_JOBS=false`, watch payments and data-request jobs in
   the scheduler and its logs. The status page will not.
5. Do not prune `uploads_data` to clear status history unless the 90-day bars
   are meant to reset.
