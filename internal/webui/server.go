// Package webui serves the citadel-logs dashboard at localhost:5500.
//
// The HTML page is server-rendered once; htmx polls a fragment endpoint every
// 5 seconds to refresh the error list for the selected service. There is no
// JavaScript build step — all assets are embedded into the binary via
// embed.FS so the Docker image stays a single static binary plus distroless.
package webui

import (
	"context"
	"embed"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/ClusterBox/citadel/internal/ingest"
	"github.com/ClusterBox/citadel/internal/logsdb"
)

//go:embed templates/*.html static/*
var assetsFS embed.FS

const (
	// recentBadgeWindow is the time window for the left-rail badge counts.
	recentBadgeWindow = 7 * 24 * time.Hour
	// errorListLimit caps how many error rows the fragment endpoint returns.
	errorListLimit = 100
)

// Server is the HTTP entrypoint for the dashboard.
type Server struct {
	db        *logsdb.DB
	factory   ingest.LogsClientFactory
	templates *template.Template
}

// New constructs a Server. Templates are parsed once at startup; the embedded
// FS is read-only so there's no hot-reload concern.
func New(db *logsdb.DB, factory ingest.LogsClientFactory) (*Server, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"fmtTime": func(ms int64) string {
			return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
		},
		"statusClass": func(status int) string {
			switch {
			case status >= 500:
				return "status-5xx"
			case status >= 400:
				return "status-4xx"
			default:
				return "status-none"
			}
		},
	}).ParseFS(assetsFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Server{db: db, factory: factory, templates: tpl}, nil
}

// Handler returns the wired http.Handler. Routes:
//
//	GET /                          — redirect to /logs
//	GET /logs                      — main dashboard
//	GET /logs/services             — htmx fragment for the rail
//	GET /logs/stream               — SSE live logs tail
//	GET /logs/errors               — htmx fragment for selected service
//	GET /logs/error/{id}           — htmx fragment for one error's detail
//	GET /api/services              — JSON
//	GET /healthz                   — liveness
//	GET /static/*                  — embedded css/js
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS())))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/logs", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /logs", s.handleDashboard)
	mux.HandleFunc("GET /logs/services", s.handleServicesFragment)
	mux.HandleFunc("GET /logs/stream", s.handleStream)
	mux.HandleFunc("GET /logs/errors", s.handleErrorsFragment)
	mux.HandleFunc("GET /logs/error/{id}", s.handleErrorDetail)
	return mux
}

func staticFS() fs.FS {
	sub, err := fs.Sub(assetsFS, ".")
	if err != nil {
		panic(err) // unreachable: assetsFS is built at compile time
	}
	return sub
}

type dashboardView struct {
	Services []serviceView
	Selected string
}

type serviceView struct {
	ID         string
	Name       string
	Env        string
	Runtime    string
	ErrorCount int
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	view, err := s.buildDashboard(ctx, r.URL.Query().Get("service"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "dashboard.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildDashboard(ctx context.Context, selected string) (dashboardView, error) {
	services, err := s.db.ListServices(ctx)
	if err != nil {
		return dashboardView{}, err
	}
	since := time.Now().Add(-recentBadgeWindow).UnixMilli()
	counts, err := s.db.CountByService(ctx, since)
	if err != nil {
		return dashboardView{}, err
	}
	view := dashboardView{Selected: selected}
	for _, svc := range services {
		view.Services = append(view.Services, serviceView{
			ID: svc.ID, Name: svc.Name, Env: svc.Env, Runtime: svc.Runtime,
			ErrorCount: counts[svc.ID],
		})
	}
	if view.Selected == "" && len(view.Services) > 0 {
		view.Selected = view.Services[0].ID
	}
	return view, nil
}

func (s *Server) handleServicesFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	view, err := s.buildDashboard(ctx, r.URL.Query().Get("service"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "services_fragment.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	serviceID := r.URL.Query().Get("service")
	if serviceID == "" {
		http.Error(w, "service param required", http.StatusBadRequest)
		return
	}
	
	services, err := s.db.ListServices(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var svc *logsdb.Service
	for _, srv := range services {
		if srv.ID == serviceID {
			svc = &srv
			break
		}
	}
	if svc == nil {
		http.NotFound(w, r)
		return
	}
	
	awsClient, err := s.factory(r.Context(), svc.Region)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	startMs := time.Now().Add(-5 * time.Minute).UnixMilli() // start from 5 min ago
	var nextToken *string

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// initial push of a connected message
	fmt.Fprintf(w, "data: <tr><td class=\"ts\">now</td><td class=\"message\" style=\"color:var(--ok)\">connected to %s</td></tr>\n\n", html.EscapeString(svc.Name))
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			endMs := time.Now().UnixMilli()
			if endMs <= startMs {
				continue
			}

			maxTS := startMs

			for {
				page, err := awsClient.FilterEvents(r.Context(), svc.LogGroup, startMs, endMs, 1000, nextToken)
				if err != nil {
					break
				}
				for _, e := range page.Events {
					if *e.Timestamp > maxTS {
						maxTS = *e.Timestamp
					}
					ts := time.UnixMilli(*e.Timestamp).Format("15:04:05")
					msg := *e.Message

					fmt.Fprintf(w, "data: <tr><td class=\"ts\">%s</td><td class=\"message\" style=\"white-space:pre-wrap; font-family:ui-monospace, 'SF Mono', Menlo, monospace; word-break:break-all\">%s</td></tr>\n\n", ts, html.EscapeString(msg))
				}
				flusher.Flush()

				if page.NextToken == nil {
					break
				}
				nextToken = page.NextToken
			}
			
			if maxTS > startMs {
				startMs = maxTS + 1 // Advance past the latest seen event
			} else {
				startMs = endMs // No events seen, advance to endMs
			}
			nextToken = nil
		}
	}
}

type errorRow struct {
	ID        int64
	TS        int64
	Status    int
	Level     string
	Message   string
	RequestID string
}

type errorsFragmentView struct {
	ServiceID string
	Errors    []errorRow
}

func (s *Server) handleErrorsFragment(w http.ResponseWriter, r *http.Request) {
	serviceID := r.URL.Query().Get("service")
	if serviceID == "" {
		http.Error(w, "service param required", http.StatusBadRequest)
		return
	}
	events, err := s.db.RecentErrors(r.Context(), serviceID, errorListLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view := errorsFragmentView{ServiceID: serviceID}
	for _, e := range events {
		view.Errors = append(view.Errors, errorRow{
			ID:        e.ID,
			TS:        e.TS,
			Status:    int(e.Status.Int64),
			Level:     e.Level.String,
			Message:   e.Message,
			RequestID: e.RequestID.String,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "errors_fragment.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type errorDetailView struct {
	ID        int64
	TS        int64
	Status    int
	Level     string
	Message   string
	RequestID string
	Stack     string
	Raw       string
	LogStream string
}

func (s *Server) handleErrorDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	e, err := s.db.ErrorByID(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if e == nil {
		http.NotFound(w, r)
		return
	}
	view := errorDetailView{
		ID: e.ID, TS: e.TS, Status: int(e.Status.Int64), Level: e.Level.String,
		Message: e.Message, RequestID: e.RequestID.String, Stack: e.Stack.String,
		Raw: e.Raw, LogStream: e.LogStream.String,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "error_detail.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
