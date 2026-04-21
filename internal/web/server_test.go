package web

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvoisin/swic/internal/calibre"
	_ "modernc.org/sqlite"
)

// newFixtureServer builds a minimal Calibre library on disk, opens it, and
// wraps it in an httptest.Server. The returned cleanup is registered with t.
func newFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()

	mustWrite := func(rel string, data []byte) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("Ada Lovelace/Alpha (1)/cover.jpg", []byte("JPEGDATA"))
	mustWrite("Ada Lovelace/Alpha (1)/Alpha - Ada Lovelace.epub", []byte("EPUBDATA"))

	dbPath := filepath.Join(dir, "metadata.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT, sort TEXT, author_sort TEXT,
			has_cover INTEGER DEFAULT 0, timestamp TEXT, pubdate TEXT, series_index REAL, path TEXT)`,
		`CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE books_authors_link (id INTEGER PRIMARY KEY, book INTEGER, author INTEGER)`,
		`CREATE TABLE series (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE books_series_link (id INTEGER PRIMARY KEY, book INTEGER, series INTEGER)`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE books_tags_link (id INTEGER PRIMARY KEY, book INTEGER, tag INTEGER)`,
		`CREATE TABLE publishers (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE books_publishers_link (id INTEGER PRIMARY KEY, book INTEGER, publisher INTEGER)`,
		`CREATE TABLE languages (id INTEGER PRIMARY KEY, lang_code TEXT)`,
		`CREATE TABLE books_languages_link (id INTEGER PRIMARY KEY, book INTEGER, lang_code INTEGER, item_order INTEGER DEFAULT 0)`,
		`CREATE TABLE identifiers (id INTEGER PRIMARY KEY, book INTEGER, type TEXT, val TEXT)`,
		`CREATE TABLE data (id INTEGER PRIMARY KEY, book INTEGER, format TEXT, uncompressed_size INTEGER, name TEXT)`,
		`CREATE TABLE comments (id INTEGER PRIMARY KEY, book INTEGER, text TEXT)`,
		`INSERT INTO books(id,title,sort,author_sort,has_cover,timestamp,pubdate,series_index,path)
			VALUES (1,'Alpha','Alpha','Lovelace, Ada',1,
			        '2024-01-02 10:00:00.000000+00:00','2023-06-01 00:00:00.000000+00:00',
			        1.0,'Ada Lovelace/Alpha (1)')`,
		`INSERT INTO authors(id,name) VALUES (1,'Ada Lovelace')`,
		`INSERT INTO books_authors_link(book,author) VALUES (1,1)`,
		`INSERT INTO data(book,format,uncompressed_size,name)
			VALUES (1,'EPUB',8,'Alpha - Ada Lovelace')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup: %v\nstmt: %s", err, s)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	lib, err := calibre.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	srv, err := New(lib, slog.New(slog.DiscardHandler), 0)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp, string(body)
}

func TestRouteRedirect(t *testing.T) {
	ts := newFixtureServer(t)
	// Disable redirect following.
	c := *ts.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/books" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHealthz(t *testing.T) {
	ts := newFixtureServer(t)
	resp, body := get(t, ts, "/healthz")
	if resp.StatusCode != http.StatusOK || body != "ok" {
		t.Errorf("status=%d body=%q", resp.StatusCode, body)
	}
}

func TestHandleList(t *testing.T) {
	ts := newFixtureServer(t)
	resp, body := get(t, ts, "/books")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(body, "Alpha") {
		t.Errorf("body missing Alpha; got: %s", body)
	}
	if !strings.Contains(body, "Ada Lovelace") {
		t.Errorf("body missing author")
	}
}

func TestHandleListSearchMiss(t *testing.T) {
	ts := newFixtureServer(t)
	_, body := get(t, ts, "/books?q=zzznotfound")
	if strings.Contains(body, ">Alpha<") {
		t.Errorf("Alpha should not appear in miss page")
	}
}

func TestHandleDetail(t *testing.T) {
	ts := newFixtureServer(t)
	resp, body := get(t, ts, "/books/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, want := range []string{"Alpha", "Ada Lovelace", "EPUB"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleDetailNotFound(t *testing.T) {
	ts := newFixtureServer(t)
	resp, _ := get(t, ts, "/books/9999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleDetailBadID(t *testing.T) {
	ts := newFixtureServer(t)
	for _, p := range []string{"/books/0", "/books/-1", "/books/abc"} {
		resp, _ := get(t, ts, p)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestHandleCover(t *testing.T) {
	ts := newFixtureServer(t)
	resp, body := get(t, ts, "/books/1/cover")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body != "JPEGDATA" {
		t.Errorf("body = %q", body)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestHandleCoverMissing(t *testing.T) {
	ts := newFixtureServer(t)
	resp, _ := get(t, ts, "/books/9999/cover")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleDownload(t *testing.T) {
	ts := newFixtureServer(t)
	resp, body := get(t, ts, "/books/1/download/epub")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body != "EPUBDATA" {
		t.Errorf("body = %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	want := `attachment; filename="Alpha - Ada Lovelace.epub"`
	if got := resp.Header.Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}

func TestHandleDownloadCaseInsensitive(t *testing.T) {
	ts := newFixtureServer(t)
	resp, _ := get(t, ts, "/books/1/download/EPUB")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleDownloadBadFormat(t *testing.T) {
	ts := newFixtureServer(t)
	for _, p := range []string{"/books/1/download/mobi", "/books/1/download/ep%2Fub"} {
		resp, _ := get(t, ts, p)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, resp.StatusCode)
		}
	}
}
