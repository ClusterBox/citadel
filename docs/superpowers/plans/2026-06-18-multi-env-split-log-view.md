# Multi-environment split log view Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Selecting a server in the citadel-logs dashboard splits the content area into one column per environment, each streaming that environment's logs.

**Architecture:** Each app-in-an-environment is already a distinct service row sharing a common `Name` (`ID = "<name>-<env>"`). Selection moves from per-service-`ID` (`?service=`) to per-app-`Name` (`?app=`). `buildDashboard` resolves the selected Name to all its service rows and renders one column each; the per-column Errors/Live-tail wiring reuses the existing `/logs/errors` and `/logs/stream` endpoints unchanged, each column passing its own service ID.

**Tech Stack:** Go (`net/http`, `html/template`, embedded FS), htmx + SSE extension, SQLite (logsdb), no JS build step.

## Global Constraints

- No new dependencies; assets stay embedded via `embed.FS` (single static binary).
- The stream/errors/detail endpoints (`handleStream`, `handleErrorsFragment`, `handleErrorDetail`) MUST remain keyed by a single service `ID` and MUST NOT change.
- Templates are parsed once at startup from the embedded FS (`ParseFS`); template names are referenced via `{{template "<name>" .}}` / `ExecuteTemplate`.
- Env column order is stable alphabetical by `Env` (no prod-priority logic).
- Run all tests with `go test ./internal/webui/...` from repo root `/home/alphauser/Documents/github/tools/citadel`.

---

### Task 1: Name-based dashboard model and `?app=` selection

Reshape the server-side view model so the dashboard is assembled around an app
`Name` (a group of env-columns) instead of a single service ID, and switch the
two dashboard handlers to read `?app=`. This task is server-side only; templates
are updated in Task 2 and Task 3, so after this task the existing templates will
still compile against the reshaped view (the field names below are chosen to keep
`services_fragment.html` rendering until Task 2 rewrites it).

**Files:**
- Modify: `internal/webui/server.go` (`dashboardView`, `serviceView`, `buildDashboard`, `handleDashboard`, `handleServicesFragment`)
- Test: `internal/webui/server_test.go`

**Interfaces:**
- Consumes: `db.ListServices(ctx) ([]logsdb.Service, error)` where `logsdb.Service` has fields `ID, Name, Env, Region, Runtime, LogGroup, RepoPath` (all strings); `db.CountByService(ctx, since int64) (map[string]int, error)` keyed by service ID.
- Produces (used by Task 2 and Task 3 templates):
  - `type appView struct { Name string; Envs string; ErrorCount int }` — one rail entry per app. `Envs` is a "·"-joined, alphabetical list of env names (e.g. `"dev · prod"`). `ErrorCount` is the summed count across that app's services.
  - `type serviceView struct { ID, Name, Env, Runtime string; ErrorCount int }` — one env-column.
  - `type dashboardView struct { Apps []appView; Columns []serviceView; Selected string }` — `Apps` drives the rail, `Columns` drives the content split, `Selected` is the selected app Name.
  - `func (s *Server) buildDashboard(ctx context.Context, app string) (dashboardView, error)`.

- [ ] **Step 1: Write the failing tests**

Add these tests to `internal/webui/server_test.go`:

```go
func TestBuildDashboardGroupsByName(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg-p", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-dev", Name: "smaug", Env: "dev", Region: "r", Runtime: "lambda", LogGroup: "lg-d", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "legolas-prod", Name: "legolas", Env: "prod", Region: "r", Runtime: "ecs", LogGroup: "lg-l", RepoPath: "/r"})

	view, err := srv.buildDashboard(ctx, "smaug")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Apps) != 2 {
		t.Fatalf("expected 2 app rail entries, got %d", len(view.Apps))
	}
	if view.Selected != "smaug" {
		t.Fatalf("expected Selected=smaug, got %q", view.Selected)
	}
	if len(view.Columns) != 2 {
		t.Fatalf("expected 2 env columns for smaug, got %d", len(view.Columns))
	}
	// alphabetical by env: dev before prod
	if view.Columns[0].Env != "dev" || view.Columns[1].Env != "prod" {
		t.Fatalf("columns not alphabetical by env: %q, %q", view.Columns[0].Env, view.Columns[1].Env)
	}
	if view.Columns[0].ID != "smaug-dev" || view.Columns[1].ID != "smaug-prod" {
		t.Fatalf("unexpected column IDs: %q, %q", view.Columns[0].ID, view.Columns[1].ID)
	}
}

func TestBuildDashboardDefaultsToFirstApp(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "aragorn-prod", Name: "aragorn", Env: "prod", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})

	view, err := srv.buildDashboard(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// apps are ordered by name; aragorn sorts first
	if view.Selected != "aragorn" {
		t.Fatalf("expected default Selected=aragorn, got %q", view.Selected)
	}
	if len(view.Columns) != 1 || view.Columns[0].ID != "aragorn-prod" {
		t.Fatalf("expected single aragorn-prod column, got %#v", view.Columns)
	}
}

func TestBuildDashboardSingleEnvOneColumn(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})

	view, err := srv.buildDashboard(ctx, "smaug")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Columns) != 1 {
		t.Fatalf("expected 1 column for single-env app, got %d", len(view.Columns))
	}
	if len(view.Apps) != 1 || view.Apps[0].Envs != "prod" {
		t.Fatalf("unexpected app rail entry: %#v", view.Apps)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/webui/ -run TestBuildDashboard -v`
Expected: FAIL to compile — `view.Apps`, `view.Columns`, and `appView` do not exist yet.

- [ ] **Step 3: Reshape the view model and `buildDashboard`**

In `internal/webui/server.go`, replace the existing `dashboardView` / `serviceView` type block (currently around lines 102-113) with:

```go
type dashboardView struct {
	Apps     []appView     // left-rail entries, one per app Name
	Columns  []serviceView // content columns, one per env of the selected app
	Selected string        // selected app Name
}

type appView struct {
	Name       string
	Envs       string // "·"-joined alphabetical env list, e.g. "dev · prod"
	ErrorCount int    // summed across the app's services
}

type serviceView struct {
	ID         string
	Name       string
	Env        string
	Runtime    string
	ErrorCount int
}
```

Replace the existing `buildDashboard` (currently around lines 128-149) with:

```go
func (s *Server) buildDashboard(ctx context.Context, app string) (dashboardView, error) {
	services, err := s.db.ListServices(ctx)
	if err != nil {
		return dashboardView{}, err
	}
	since := time.Now().Add(-recentBadgeWindow).UnixMilli()
	counts, err := s.db.CountByService(ctx, since)
	if err != nil {
		return dashboardView{}, err
	}

	// Group services by Name, preserving the ListServices order (ID-sorted).
	type group struct {
		name     string
		services []logsdb.Service
	}
	var groups []*group
	byName := map[string]*group{}
	for _, svc := range services {
		g, ok := byName[svc.Name]
		if !ok {
			g = &group{name: svc.Name}
			byName[svc.Name] = g
			groups = append(groups, g)
		}
		g.services = append(g.services, svc)
	}

	view := dashboardView{Selected: app}
	for _, g := range groups {
		envs := make([]string, 0, len(g.services))
		total := 0
		for _, svc := range g.services {
			envs = append(envs, svc.Env)
			total += counts[svc.ID]
		}
		sort.Strings(envs)
		view.Apps = append(view.Apps, appView{
			Name:       g.name,
			Envs:       strings.Join(envs, " · "),
			ErrorCount: total,
		})
	}

	if view.Selected == "" && len(view.Apps) > 0 {
		view.Selected = view.Apps[0].Name
	}

	if g, ok := byName[view.Selected]; ok {
		cols := make([]logsdb.Service, len(g.services))
		copy(cols, g.services)
		sort.Slice(cols, func(i, j int) bool { return cols[i].Env < cols[j].Env })
		for _, svc := range cols {
			view.Columns = append(view.Columns, serviceView{
				ID: svc.ID, Name: svc.Name, Env: svc.Env, Runtime: svc.Runtime,
				ErrorCount: counts[svc.ID],
			})
		}
	}
	return view, nil
}
```

Add `"sort"` and `"strings"` to the import block in `internal/webui/server.go` (alongside the existing `"strconv"`, `"time"`, etc.).

Update the two callers to read `?app=` instead of `?service=`:

In `handleDashboard` (currently line 117), change:
```go
	view, err := s.buildDashboard(ctx, r.URL.Query().Get("service"))
```
to:
```go
	view, err := s.buildDashboard(ctx, r.URL.Query().Get("app"))
```

In `handleServicesFragment` (currently line 153), change:
```go
	view, err := s.buildDashboard(ctx, r.URL.Query().Get("service"))
```
to:
```go
	view, err := s.buildDashboard(ctx, r.URL.Query().Get("app"))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/webui/ -run TestBuildDashboard -v`
Expected: PASS (all three).

Note: the full package will NOT build yet because `services_fragment.html` / `dashboard.html` still reference `.Services`. That is fixed in Task 2 and Task 3. Run the scoped `-run TestBuildDashboard` here; do not run the full package test until Task 3.

- [ ] **Step 5: Commit**

```bash
git add internal/webui/server.go internal/webui/server_test.go
git commit -m "feat(webui): name-based dashboard model and ?app= selection"
```

---

### Task 2: Rail grouped by app, linking via `?app=`

Rewrite the left-rail fragment to render one entry per app (`appView`) and link
to `/logs?app=<name>`, with the polling `hx-get` also using `?app=`.

**Files:**
- Modify: `internal/webui/templates/services_fragment.html`
- Test: `internal/webui/server_test.go`

**Interfaces:**
- Consumes: `dashboardView.Apps []appView` (fields `Name`, `Envs`, `ErrorCount`) and `dashboardView.Selected string`, from Task 1.

- [ ] **Step 1: Write the failing test**

Add to `internal/webui/server_test.go`:

```go
func TestRailGroupsAppsByName(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-dev", Name: "smaug", Env: "dev", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/logs/services?app=smaug")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	// one app link, not one per env
	if strings.Count(s, "/logs?app=smaug") != 1 {
		t.Fatalf("expected exactly one app link, body: %s", s)
	}
	if strings.Contains(s, "?service=") {
		t.Fatalf("rail should not link by service id, body: %s", s)
	}
	if !strings.Contains(s, "dev · prod") {
		t.Fatalf("expected env indicator 'dev · prod', body: %s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/webui/ -run TestRailGroupsAppsByName -v`
Expected: FAIL — current template still references `.Services` / `?service=` (and the package may not yet compile the template against the new model).

- [ ] **Step 3: Rewrite the rail fragment**

Replace the entire contents of `internal/webui/templates/services_fragment.html` with:

```html
{{define "services_fragment.html"}}
<nav class="rail" id="services-rail" hx-get="/logs/services{{if .Selected}}?app={{.Selected}}{{end}}" hx-trigger="every 5s" hx-swap="outerHTML">
  <h2>services</h2>
  {{if not .Apps}}
    <p class="muted">no services registered. add one with<br><code>citadel logs-daemon register --env dev</code></p>
  {{end}}
  <ul>
    {{range .Apps}}
    <li>
      <a href="/logs?app={{.Name}}"{{if eq .Name $.Selected}} class="active"{{end}}>
        <span class="svc-name">{{.Name}}</span>
        <span class="svc-env">{{.Envs}}</span>
        {{if .ErrorCount}}<span class="badge">{{.ErrorCount}}</span>{{end}}
      </a>
    </li>
    {{end}}
  </ul>
</nav>
{{end}}
```

Note: the rail `<a>` grid is defined for four columns (`1fr auto auto auto`) in
styles.css; with three children here the trailing track simply collapses, which
is fine. Task 4 adjusts styles.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/webui/ -run TestRailGroupsAppsByName -v`
Expected: PASS.

(The full package still won't build until Task 3 fixes `dashboard.html`. The
`/logs/services` route renders `services_fragment.html` directly, so this scoped
test passes once the template above is in place.)

- [ ] **Step 5: Commit**

```bash
git add internal/webui/templates/services_fragment.html internal/webui/server_test.go
git commit -m "feat(webui): group log rail by app, link via ?app="
```

---

### Task 3: Split content into per-env columns with independent toggles

Rewrite the dashboard content area to range over `.Columns`, rendering one column
per environment. Each column carries its own Errors / Live-Logs toggle (with
element IDs made unique per column) and wires its Errors fragment and live SSE
tail to its own service ID.

**Files:**
- Modify: `internal/webui/templates/dashboard.html`
- Test: `internal/webui/server_test.go`

**Interfaces:**
- Consumes: `dashboardView.Columns []serviceView` (fields `ID`, `Name`, `Env`, `Runtime`, `ErrorCount`) and `dashboardView.Selected string`, from Task 1.

- [ ] **Step 1: Write the failing test**

Add to `internal/webui/server_test.go`:

```go
func TestDashboardRendersColumnPerEnv(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-dev", Name: "smaug", Env: "dev", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/logs?app=smaug")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)

	// one live SSE tail per env, each keyed by its own service ID
	if !strings.Contains(s, "/logs/stream?service=smaug-dev") {
		t.Fatalf("missing dev stream wiring: %s", s)
	}
	if !strings.Contains(s, "/logs/stream?service=smaug-prod") {
		t.Fatalf("missing prod stream wiring: %s", s)
	}
	// one errors fragment per env
	if !strings.Contains(s, "/logs/errors?service=smaug-dev") ||
		!strings.Contains(s, "/logs/errors?service=smaug-prod") {
		t.Fatalf("missing per-env errors wiring: %s", s)
	}
	// per-column toggle IDs must be unique (suffixed with service id)
	if !strings.Contains(s, "errors-view-smaug-dev") ||
		!strings.Contains(s, "errors-view-smaug-prod") {
		t.Fatalf("toggle element IDs not made unique per column: %s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/webui/ -run TestDashboardRendersColumnPerEnv -v`
Expected: FAIL — current `dashboard.html` references `.Selected` as a title and a single hardcoded `errors-view`/`live-view`, and references `.Services` indirectly via the fragment (already fixed) but not per-env stream wiring.

- [ ] **Step 3: Rewrite the dashboard content area**

Replace the entire contents of `internal/webui/templates/dashboard.html` with:

```html
{{define "dashboard.html"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>citadel · logs</title>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/htmx.min.js" defer></script>
  <script src="/static/sse.js" defer></script>
</head>
<body>
  <header class="topbar">
    <strong>citadel</strong>
    <span class="muted">/ logs</span>
  </header>

  <main class="layout">
    {{template "services_fragment.html" .}}

    <section class="content">
      {{if .Columns}}
        <h1>{{.Selected}}</h1>
        <div class="env-split{{if gt (len .Columns) 1}} split{{end}}">
          {{range .Columns}}
          <div class="env-col">
            <div class="env-col-head">
              <span class="env-col-env">{{.Env}}</span>
              <span class="env-col-runtime">{{.Runtime}}</span>
              {{if .ErrorCount}}<span class="badge">{{.ErrorCount}}</span>{{end}}
            </div>

            <div style="margin-bottom: 16px; border-bottom: 1px solid var(--border); padding-bottom: 8px;">
              <button onclick="document.getElementById('errors-view-{{.ID}}').style.display='block'; document.getElementById('live-view-{{.ID}}').style.display='none';" style="background:var(--panel); color:var(--text); border:1px solid var(--border); padding:6px 12px; border-radius:4px; cursor:pointer; margin-right:8px;">Errors</button>
              <button onclick="document.getElementById('errors-view-{{.ID}}').style.display='none'; document.getElementById('live-view-{{.ID}}').style.display='block';" style="background:var(--panel); color:var(--text); border:1px solid var(--border); padding:6px 12px; border-radius:4px; cursor:pointer;">Live Logs</button>
            </div>

            <div id="errors-view-{{.ID}}" style="display:block;">
              <p class="muted">errors in the last 7 days · refreshes every 5s</p>
              <div hx-get="/logs/errors?service={{.ID}}"
                   hx-trigger="load, every 5s"
                   hx-swap="innerHTML">
                <p class="muted">loading…</p>
              </div>
            </div>

            <div id="live-view-{{.ID}}" style="display:none;">
              <p class="muted">live tailing from cloudwatch...</p>
              <table class="errors">
                <thead>
                  <tr>
                    <th>when</th>
                    <th>message</th>
                  </tr>
                </thead>
                <tbody hx-ext="sse" sse-connect="/logs/stream?service={{.ID}}" sse-swap="message" hx-swap="beforeend">
                </tbody>
              </table>
            </div>
          </div>
          {{end}}
        </div>
      {{else}}
        <h1>no service selected</h1>
        <p class="muted">register a service from a citadel.yml repo to begin observing.</p>
      {{end}}
    </section>
  </main>
</body>
</html>{{end}}
```

- [ ] **Step 4: Run the full package tests to verify everything passes**

Run: `go test ./internal/webui/ -v`
Expected: PASS — all tests, including the pre-existing `TestDashboardRendersWhenEmpty`, `TestErrorsFragmentReflectsDB`, `TestHealthz`, and the new Task 1/2/3 tests.

If `TestDashboardRendersWhenEmpty` fails on the empty-state string, confirm the
empty rail still renders "no services registered" (it does, via the `{{if not
.Apps}}` branch in Task 2).

- [ ] **Step 5: Commit**

```bash
git add internal/webui/templates/dashboard.html internal/webui/server_test.go
git commit -m "feat(webui): split log content into per-env columns with independent toggles"
```

---

### Task 4: Split-column layout styling

Add CSS so multi-env columns sit side by side and a single env spans full width,
plus styling for the per-column header.

**Files:**
- Modify: `internal/webui/static/styles.css`

**Interfaces:**
- Consumes: the `.env-split`, `.env-split.split`, `.env-col`, `.env-col-head`, `.env-col-env`, `.env-col-runtime` classes emitted by `dashboard.html` in Task 3.

- [ ] **Step 1: Append the split-layout styles**

Append to the end of `internal/webui/static/styles.css`:

```css
.env-split { display: flex; gap: 24px; align-items: flex-start; }
.env-split .env-col { flex: 1 1 0; min-width: 0; }
/* single column spans full width naturally (flex:1 with one child) */

.env-col-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.env-col-env {
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--accent);
}
.env-col-runtime { font-size: 11px; color: var(--muted); }

@media (max-width: 900px) {
  .env-split.split { flex-direction: column; }
}
```

- [ ] **Step 2: Verify the package still builds and tests pass**

Run: `go test ./internal/webui/ -v`
Expected: PASS (CSS is embedded; no behavior change to assert, this confirms the embed still compiles).

- [ ] **Step 3: Commit**

```bash
git add internal/webui/static/styles.css
git commit -m "style(webui): side-by-side env columns for split log view"
```

---

### Task 5: Full build and manual smoke verification

Confirm the whole module builds and the binary serves the new dashboard.

**Files:** none (verification only).

- [ ] **Step 1: Build the whole module**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 2: Run the full webui test suite once more**

Run: `go test ./internal/webui/...`
Expected: `ok  github.com/ClusterBox/citadel/internal/webui`.

- [ ] **Step 3: Commit (only if any incidental fixes were needed)**

If steps 1-2 required no changes, skip the commit. Otherwise:

```bash
git add -A
git commit -m "chore(webui): fixups from full-build verification"
```
