# Multi-environment split log view

**Date:** 2026-06-18
**Status:** Approved design

## Problem

The citadel-logs dashboard (`localhost:5500`) lists registered services in a left
rail and shows logs for one selected service in the content area. An app deployed
to multiple environments (e.g. smaug in `prod` and `dev`) appears as two separate
rail entries (`smaug-prod`, `smaug-dev`), so comparing the same app across
environments means clicking back and forth.

We want: selecting a server splits the content area into one column per
environment and streams each environment's logs side by side.

## Key insight

Each app-in-an-environment is already its own service row. From
`registry.go`, the resolved identity is:

- `ID = "<name>-<env>"` (e.g. `smaug-prod`)
- `Name = cfg.Name` — **shared across all environments of one app** (e.g. `smaug`)
- `Env = <env>` (e.g. `prod`, `dev`)

So pairing is by `Name`: group service rows that share a `Name`, render one
column per row. No schema change, no new persistence.

## Design

### 1. Selection becomes Name-based

The rail today lists one entry per service `ID` and links to
`/logs?service=<id>`. It will instead group services by `Name`, list one entry
per app, and link to `/logs?app=<name>`. Each app entry shows its name plus a
small indicator of which environments exist for it (e.g. `prod · dev`) and an
aggregate error badge (sum of its services' counts).

### 2. Content area renders one column per environment

`buildDashboard` resolves the selected app `Name` to all its service rows,
sorted for stable order (alphabetical by env; this puts `dev` before `prod`,
which is acceptable — stable ordering is what matters, not a specific env
priority). The content template ranges over those services:

- **1 environment** → a single full-width column (current look, no split).
- **2+ environments** → a CSS flex/grid split, one column per environment, each
  headed by its env name.

Each column independently mirrors the current tab UI — its **own** Errors /
Live Logs toggle, keyed by that env's service `ID`:

- Errors fragment: `hx-get="/logs/errors?service=<id>"` (endpoint unchanged).
- Live tail: `sse-connect="/logs/stream?service=<id>"` (endpoint unchanged).

The toggle buttons currently use hardcoded element IDs (`errors-view`,
`live-view`) and inline `onclick` handlers. With multiple columns these IDs
must be made unique per column (e.g. suffixed with the service ID) so each
column toggles independently without colliding.

### 3. Backend changes (minimal)

Endpoints that key off a single service ID are **unchanged** —
`handleStream`, `handleErrorsFragment`, `handleErrorDetail` — because each
column supplies its own service ID exactly as a single-service selection does
today.

Changes are confined to the dashboard assembly:

- `dashboardView` carries the selected app `Name` and the ordered list of that
  app's services (each with its per-service error count). A `serviceView`
  continues to represent one env-column.
- `buildDashboard(ctx, app string)` filters `ListServices` to rows where
  `Name == app`, builds the column list, and computes the rail grouping.
- `handleDashboard` and `handleServicesFragment` read `?app=` instead of
  `?service=`.
- Default selection (when no `?app=`) selects the first app by Name.

### Data flow

```
rail link: /logs?app=smaug
  → handleDashboard reads app="smaug"
  → buildDashboard("smaug")
      → ListServices()
      → group all rows by Name (for the rail)
      → filter rows where Name=="smaug" → [smaug-dev, smaug-prod]
      → CountByService → per-column badges
  → dashboard.html ranges over the app's services:
        column(dev):  errors hx-get ?service=smaug-dev  | live sse ?service=smaug-dev
        column(prod): errors hx-get ?service=smaug-prod | live sse ?service=smaug-prod
```

Each live column opens its own `EventSource`. The bundled htmx SSE extension
isolates one source per `sse-connect` element and closes it on element removal
(`maybeCloseSSESource`), so switching apps tears down each column's stream
cleanly.

## Components

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| rail grouping (server.go + services_fragment.html) | group services by Name, one app entry, link by `?app=` | `db.ListServices`, `db.CountByService` |
| column assembly (server.go) | resolve app Name → ordered service columns | `db.ListServices` |
| column rendering (dashboard.html) | render N columns, per-column unique toggle IDs, per-column errors + live tail | column assembly output |
| split layout (styles.css) | flex/grid columns; single column when N==1 | — |
| stream / errors / detail handlers | unchanged; key off one service ID per column | `db`, `factory` |

## Files touched

- `internal/webui/server.go` — reshape `dashboardView`; `buildDashboard`
  groups by Name and resolves columns; swap `service`→`app` param in
  `handleDashboard` and `handleServicesFragment`.
- `internal/webui/templates/dashboard.html` — range over columns; per-column
  unique toggle element IDs.
- `internal/webui/templates/services_fragment.html` — group rail by Name; link
  via `?app=`; env indicator + aggregate badge.
- `internal/webui/static/styles.css` — split-column layout.
- `internal/webui/server_test.go` — update for app-based selection and
  multi-column rendering.

## Edge cases

- App deployed to one env → single full-width column, no empty split.
- `?app=` missing → default to first app (current behavior with first service).
- `?app=` not found → "no service selected" empty state (current behavior).
- Switching apps → each column's `EventSource` is torn down by the SSE
  extension on DOM removal.

## Out of scope (YAGNI)

- Merged/interleaved single-stream view across envs.
- Configurable env ordering / env priority.
- Placeholder columns for environments where the app is not deployed.
- Any change to ingestion, persistence, or the stream/errors/detail endpoints.

## Testing

- `buildDashboard` groups multiple service rows sharing a Name into one rail
  entry and N columns; single-Name app yields one column.
- Default selection picks the first app when `?app=` is absent.
- Rendered dashboard contains one errors block and one live `sse-connect` per
  environment, each carrying the correct service ID.
- Per-column toggle element IDs are unique across columns.
