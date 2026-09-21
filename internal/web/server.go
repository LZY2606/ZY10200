// Package web 提供光回波事件室的本地 HTTP 服务与操作页面。
package web

import (
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"

	"otdrroom/internal/app"
	"otdrroom/internal/otdr"
)

//go:embed templates/*.html static/*.css
var assets embed.FS

// Server 持有应用服务与模板。
type Server struct {
	svc *app.Service
	tpl *template.Template
}

// NewServer 构造 HTTP 服务。
func NewServer(svc *app.Service) (*Server, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"traceSVG": func(it otdr.Interpretation) template.HTML {
			return template.HTML(RenderTraceSVG(it))
		},
		"overlaySVG": func(p app.AlignedPair) template.HTML {
			return template.HTML(RenderOverlaySVG(p))
		},
		"eventLabel": func(k string) string {
			return eventLabelZh(k)
		},
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{svc: svc, tpl: tpl}, nil
}

// Routes 注册全部路由。
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/traces/", s.handleTraceAction)
	mux.HandleFunc("/links", s.handleLinks)
	mux.HandleFunc("/links/delete", s.handleLinkDelete)
	mux.HandleFunc("/reimport", s.handleReimport)
	mux.HandleFunc("/runs/export", s.handleExport)
	return logRequests(mux)
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.RequestURI())
		h.ServeHTTP(w, r)
	})
}

func (s *Server) flash(w http.ResponseWriter, r *http.Request, msg string, code int) {
	if code >= 400 {
		http.Error(w, msg, code)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func ctx(r *http.Request) context.Context { return r.Context() }
