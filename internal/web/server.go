package web

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/jvoisin/swic/internal/calibre"
)

// Server bundles the dependencies required by the HTTP handlers.
type Server struct {
	lib       *calibre.Library
	templates *template.Template
	staticFS  fs.FS
	logger    *slog.Logger
	pageSize  int
}

// New constructs a Server backed by the given library.
// pageSize controls how many books are shown per page (0 defaults to 50).
func New(lib *calibre.Library, logger *slog.Logger, pageSize int) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	staticDir, err := staticSubFS()
	if err != nil {
		return nil, fmt.Errorf("web: init static fs: %w", err)
	}
	return &Server{lib: lib, templates: tmpl, staticFS: staticDir, logger: logger, pageSize: pageSize}, nil
}

// Handler returns the configured HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/books", http.StatusFound)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /books", s.handleList)
	mux.HandleFunc("GET /books/{id}", s.handleDetail)
	mux.HandleFunc("GET /books/{id}/cover", s.handleCover)
	mux.HandleFunc("GET /books/{id}/download/{format}", s.handleDownload)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(s.staticFS)))

	return mux
}

func (s *Server) renderError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	data := map[string]any{"Status": status, "Message": msg}
	if err := s.templates.ExecuteTemplate(w, "error.gohtml", data); err != nil {
		s.logger.Error("render error template", "err", err)
	}
}

func (s *Server) notFound(w http.ResponseWriter) {
	s.renderError(w, http.StatusNotFound, "Not found")
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render template", "tmpl", name, "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// notFound or generic error helper used by handlers.
func (s *Server) handleErr(w http.ResponseWriter, err error, where string) {
	if errors.Is(err, calibre.ErrNotFound) {
		s.renderError(w, http.StatusNotFound, "Not found")
		return
	}
	s.logger.Error("handler error", "where", where, "err", err)
	s.renderError(w, http.StatusInternalServerError, "Internal server error")
}
