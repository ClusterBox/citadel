package webui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/ClusterBox/citadel/internal/deploydb"
)

// DeployServer serves the local deployment-history dashboard. It reuses the
// embedded htmx/static assets but is backed by deploydb instead of logsdb.
type DeployServer struct {
	db        *deploydb.DB
	templates *template.Template
}

// NewDeployServer parses the deployments template and returns a server.
func NewDeployServer(db *deploydb.DB) (*DeployServer, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"fmtTime": func(ms int64) string {
			return time.UnixMilli(ms).Format("2006-01-02 15:04")
		},
	}).ParseFS(assetsFS, "templates/deployments.html")
	if err != nil {
		return nil, fmt.Errorf("parse deployments template: %w", err)
	}
	return &DeployServer{db: db, templates: tpl}, nil
}

// Handler wires the routes:
//
//	GET /                  -> redirect to /deployments
//	GET /static/...        -> embedded assets
//	GET /healthz           -> "ok"
//	GET /deployments       -> full page
//	GET /deployments/rows  -> htmx tbody fragment (filterable)
func (s *DeployServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS())))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/deployments", http.StatusFound)
	})
	mux.HandleFunc("GET /deployments", s.handlePage)
	mux.HandleFunc("GET /deployments/rows", s.handleRows)
	return mux
}

type deployRow struct {
	deploydb.Deployment
	Took string
}

type deployView struct {
	Project string
	Env     string
	Rows    []deployRow
}

func (s *DeployServer) view(ctx context.Context, project, env string) (deployView, error) {
	recs, err := s.db.List(ctx, deploydb.Filter{Project: project, Env: env, Limit: 200})
	if err != nil {
		return deployView{}, err
	}
	rows := make([]deployRow, 0, len(recs))
	for _, r := range recs {
		took := "—"
		if r.DurationMS != nil {
			took = (time.Duration(*r.DurationMS) * time.Millisecond).Round(time.Second).String()
		}
		rows = append(rows, deployRow{Deployment: r, Took: took})
	}
	return deployView{Project: project, Env: env, Rows: rows}, nil
}

func (s *DeployServer) handlePage(w http.ResponseWriter, r *http.Request) {
	v, err := s.view(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("env"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.templates.ExecuteTemplate(w, "deployments_page", v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *DeployServer) handleRows(w http.ResponseWriter, r *http.Request) {
	v, err := s.view(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("env"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.templates.ExecuteTemplate(w, "deployments_rows", v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
