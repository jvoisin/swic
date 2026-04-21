package web

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/jvoisin/swic/internal/calibre"
)

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	page = max(1, page)

	sort := calibre.SortOrder(query.Get("sort"))
	switch sort {
	case calibre.SortByTitle, calibre.SortByAuthor, calibre.SortByDate, calibre.SortByLastRead:
	default:
		sort = calibre.SortByDate
	}

	field := calibre.SearchField(query.Get("in"))
	switch field {
	case calibre.SearchTitle, calibre.SearchAuthor, calibre.SearchSeries, calibre.SearchTag, calibre.SearchPublisher:
	default:
		field = calibre.SearchAny
	}

	search := strings.TrimSpace(query.Get("q"))
	if len(search) > 1000 {
		search = search[:1000]
	}

	q := calibre.ListQuery{
		Limit:    s.pageSize,
		Offset:   (page - 1) * s.pageSize,
		Sort:     sort,
		Search:   search,
		SearchIn: field,
	}
	books, total, err := s.lib.ListBooks(r.Context(), q)
	if err != nil {
		s.handleErr(w, err, "list")
		return
	}
	totalPages := max((total+s.pageSize-1)/s.pageSize, 1)
	data := map[string]any{
		"Books":        books,
		"Total":        total,
		"LibraryTotal": s.lib.BookCount(),
		"Page":       page,
		"TotalPages": totalPages,
		"Sort":       string(sort),
		"Query":      q.Search,
		"SearchIn":   string(field),
		"HasPrev":    page > 1,
		"HasNext":    page < totalPages,
		"PrevPage":   page - 1,
		"NextPage":   page + 1,
	}
	s.render(w, "books_list.gohtml", data)
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := parseBookID(r)
	if !ok {
		s.notFound(w)
		return
	}
	book, err := s.lib.GetBook(r.Context(), id)
	if err != nil {
		s.handleErr(w, err, "detail")
		return
	}
	s.render(w, "book_detail.gohtml", map[string]any{"Book": book})
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	id, ok := parseBookID(r)
	if !ok {
		s.notFound(w)
		return
	}
	rel, err := s.lib.CoverPath(r.Context(), id)
	if err != nil {
		s.handleErr(w, err, "cover")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFileFS(w, r, s.lib.FS(), rel)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := parseBookID(r)
	if !ok {
		s.notFound(w)
		return
	}
	format := r.PathValue("format")
	if !validFormat(format) {
		s.notFound(w)
		return
	}
	rel, err := s.lib.BookFilePath(r.Context(), id, format)
	if err != nil {
		s.handleErr(w, err, "download")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, sanitizeFilename(path.Base(rel))))
	http.ServeFileFS(w, r, s.lib.FS(), rel)
}

// parseBookID extracts the {id} path value and validates it is a positive int.
func parseBookID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// validFormat ensures the format path segment is a short alphanumeric string.
func validFormat(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// sanitizeFilename strips characters that are unsafe in HTTP headers.
func sanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '"' {
			return -1
		}
		return r
	}, s)
	if s == "" {
		s = "book"
	}
	return s
}
