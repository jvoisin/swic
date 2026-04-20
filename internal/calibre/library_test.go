package calibre

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// newFixtureLibrary builds a minimal Calibre-shaped library on disk and opens it.
// It registers cleanup with t.
//
// Books fixture:
//
//	id=1 "Alpha"   author "Ada Lovelace"  series "Foundation" #1.0
//	     tag "sci-fi"  publisher "Acme"   lang "eng"
//	     identifier isbn=111  formats: epub (123 B) + pdf (456 B)
//	     comments: "<p>Hello <b>World</b></p>"   has_cover=1
//	id=2 "Beta"    author "Ben Franklin"  no series
//	     tag "history" publisher "Acme"   lang "eng"
//	     identifier isbn=222  format: epub (789 B)   has_cover=0
//	id=3 "Gamma"   author "Cara Doe"      no series, no tags, no publisher
//	     no formats, no cover.
func newFixtureLibrary(t *testing.T) *Library {
	t.Helper()
	dir := t.TempDir()

	// Create on-disk file structure for the books that have files.
	mustWrite := func(rel string, data []byte) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("Ada Lovelace/Alpha (1)/cover.jpg", make([]byte, 8))
	mustWrite("Ada Lovelace/Alpha (1)/Alpha - Ada Lovelace.epub", make([]byte, 123))
	mustWrite("Ada Lovelace/Alpha (1)/Alpha - Ada Lovelace.pdf", make([]byte, 456))
	mustWrite("Ben Franklin/Beta (2)/Beta - Ben Franklin.epub", make([]byte, 789))

	dbPath := filepath.Join(dir, "metadata.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE books (
			id INTEGER PRIMARY KEY, title TEXT, sort TEXT, author_sort TEXT,
			has_cover INTEGER DEFAULT 0, timestamp TEXT, pubdate TEXT,
			series_index REAL, path TEXT)`,
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
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v\nstmt: %s", err, s)
		}
	}

	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("insert: %v\nq: %s", err, q)
		}
	}
	// books
	exec(`INSERT INTO books(id,title,sort,author_sort,has_cover,timestamp,pubdate,series_index,path) VALUES
		(1,'Alpha','Alpha','Lovelace, Ada',1,'2024-01-02 10:00:00.000000+00:00','2023-06-01 00:00:00.000000+00:00',1.0,'Ada Lovelace/Alpha (1)'),
		(2,'Beta','Beta','Franklin, Ben',0,'2024-02-03 11:00:00.000000+00:00','2022-01-01 00:00:00.000000+00:00',1.0,'Ben Franklin/Beta (2)'),
		(3,'Gamma','Gamma','Doe, Cara',0,'2024-03-04 12:00:00.000000+00:00','',1.0,'Cara Doe/Gamma (3)')`)
	// authors
	exec(`INSERT INTO authors(id,name) VALUES (1,'Ada Lovelace'),(2,'Ben Franklin'),(3,'Cara Doe')`)
	exec(`INSERT INTO books_authors_link(book,author) VALUES (1,1),(2,2),(3,3)`)
	// series
	exec(`INSERT INTO series(id,name) VALUES (1,'Foundation')`)
	exec(`INSERT INTO books_series_link(book,series) VALUES (1,1)`)
	// tags
	exec(`INSERT INTO tags(id,name) VALUES (1,'sci-fi'),(2,'history')`)
	exec(`INSERT INTO books_tags_link(book,tag) VALUES (1,1),(2,2)`)
	// publishers
	exec(`INSERT INTO publishers(id,name) VALUES (1,'Acme')`)
	exec(`INSERT INTO books_publishers_link(book,publisher) VALUES (1,1),(2,1)`)
	// languages
	exec(`INSERT INTO languages(id,lang_code) VALUES (1,'eng')`)
	exec(`INSERT INTO books_languages_link(book,lang_code,item_order) VALUES (1,1,0),(2,1,0)`)
	// identifiers
	exec(`INSERT INTO identifiers(book,type,val) VALUES (1,'isbn','111'),(2,'isbn','222')`)
	// data (formats)
	exec(`INSERT INTO data(book,format,uncompressed_size,name) VALUES
		(1,'EPUB',123,'Alpha - Ada Lovelace'),
		(1,'PDF',456,'Alpha - Ada Lovelace'),
		(2,'EPUB',789,'Beta - Ben Franklin')`)
	// comments
	exec(`INSERT INTO comments(book,text) VALUES (1,'<p>Hello <b>World</b></p>')`)

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	lib, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
}

func TestListBooks(t *testing.T) {
	lib := newFixtureLibrary(t)
	ctx := context.Background()

	t.Run("default sort by date desc", func(t *testing.T) {
		books, total, err := lib.ListBooks(ctx, ListQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(books) != 3 {
			t.Fatalf("len(books) = %d, want 3", len(books))
		}
		want := []string{"Gamma", "Beta", "Alpha"}
		for i, b := range books {
			if b.Title != want[i] {
				t.Errorf("books[%d].Title = %q, want %q", i, b.Title, want[i])
			}
		}
		if got := books[2].Authors; len(got) != 1 || got[0] != "Ada Lovelace" {
			t.Errorf("Alpha authors = %v", got)
		}
		if !books[2].HasCover {
			t.Errorf("Alpha should have cover")
		}
		if books[1].HasCover {
			t.Errorf("Beta should not have cover")
		}
		if books[2].SeriesName != "Foundation" {
			t.Errorf("Alpha series = %q", books[2].SeriesName)
		}
		if books[0].SeriesName != "" {
			t.Errorf("Gamma series should be empty, got %q", books[0].SeriesName)
		}
	})

	t.Run("sort by title", func(t *testing.T) {
		books, _, err := lib.ListBooks(ctx, ListQuery{Sort: SortByTitle})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{books[0].Title, books[1].Title, books[2].Title}
		want := []string{"Alpha", "Beta", "Gamma"}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("title sort: pos %d got %q want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("limit and offset", func(t *testing.T) {
		books, total, err := lib.ListBooks(ctx, ListQuery{Limit: 1, Offset: 1})
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3 (window count)", total)
		}
		if len(books) != 1 || books[0].Title != "Beta" {
			t.Errorf("page = %+v", books)
		}
	})

	t.Run("search by title", func(t *testing.T) {
		books, total, err := lib.ListBooks(ctx, ListQuery{Search: "alp", SearchIn: SearchTitle})
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || len(books) != 1 || books[0].Title != "Alpha" {
			t.Errorf("got total=%d books=%+v", total, books)
		}
	})

	t.Run("search by author", func(t *testing.T) {
		_, total, err := lib.ListBooks(ctx, ListQuery{Search: "franklin", SearchIn: SearchAuthor})
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1", total)
		}
	})

	t.Run("search by tag", func(t *testing.T) {
		_, total, err := lib.ListBooks(ctx, ListQuery{Search: "sci", SearchIn: SearchTag})
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1", total)
		}
	})

	t.Run("search by publisher", func(t *testing.T) {
		_, total, err := lib.ListBooks(ctx, ListQuery{Search: "acme", SearchIn: SearchPublisher})
		if err != nil {
			t.Fatal(err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2", total)
		}
	})

	t.Run("search any", func(t *testing.T) {
		_, total, err := lib.ListBooks(ctx, ListQuery{Search: "Foundation"})
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1", total)
		}
	})

	t.Run("offset past end", func(t *testing.T) {
		books, total, err := lib.ListBooks(ctx, ListQuery{Offset: 999})
		if err != nil {
			t.Fatal(err)
		}
		if len(books) != 0 || total != 0 {
			t.Errorf("expected empty page, got books=%v total=%d", books, total)
		}
	})
}

func TestGetBook(t *testing.T) {
	lib := newFixtureLibrary(t)
	ctx := context.Background()

	b, err := lib.GetBook(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Alpha" {
		t.Errorf("Title = %q", b.Title)
	}
	if len(b.Authors) != 1 || b.Authors[0] != "Ada Lovelace" {
		t.Errorf("Authors = %v", b.Authors)
	}
	if b.SeriesName != "Foundation" {
		t.Errorf("SeriesName = %q", b.SeriesName)
	}
	if b.Publisher != "Acme" {
		t.Errorf("Publisher = %q", b.Publisher)
	}
	if !b.HasCover {
		t.Errorf("HasCover should be true")
	}
	if got, want := b.Tags, []string{"sci-fi"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Tags = %v", got)
	}
	if got := b.Languages; len(got) != 1 || got[0] != "eng" {
		t.Errorf("Languages = %v", got)
	}
	if len(b.Identifiers) != 1 || b.Identifiers[0].Type != "isbn" || b.Identifiers[0].Value != "111" {
		t.Errorf("Identifiers = %+v", b.Identifiers)
	}
	if len(b.Formats) != 2 {
		t.Fatalf("len(Formats) = %d, want 2", len(b.Formats))
	}
	// data sorted by format ASC -> EPUB, PDF
	if b.Formats[0].Format != "EPUB" || b.Formats[0].SizeBytes != 123 {
		t.Errorf("Formats[0] = %+v", b.Formats[0])
	}
	if b.Description != "Hello World" {
		t.Errorf("Description = %q", b.Description)
	}
	if b.PubDate.IsZero() {
		t.Errorf("PubDate should be parsed")
	}

	// Book with no series, no comments, no publisher.
	b3, err := lib.GetBook(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if b3.SeriesName != "" || b3.Publisher != "" || b3.Description != "" {
		t.Errorf("expected empty optional fields, got %+v", b3)
	}
	if !b3.PubDate.IsZero() {
		t.Errorf("empty pubdate should parse to zero, got %v", b3.PubDate)
	}
	if len(b3.Tags) != 0 || len(b3.Formats) != 0 || len(b3.Identifiers) != 0 {
		t.Errorf("expected empty slices, got %+v", b3)
	}
}

func TestGetBookNotFound(t *testing.T) {
	lib := newFixtureLibrary(t)
	_, err := lib.GetBook(context.Background(), 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCoverPath(t *testing.T) {
	lib := newFixtureLibrary(t)
	ctx := context.Background()

	rel, err := lib.CoverPath(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "Ada Lovelace/Alpha (1)/cover.jpg" {
		t.Errorf("rel = %q", rel)
	}

	if _, err := lib.CoverPath(ctx, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("book without cover: err = %v, want ErrNotFound", err)
	}
	if _, err := lib.CoverPath(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing book: err = %v, want ErrNotFound", err)
	}
}

func TestBookFilePath(t *testing.T) {
	lib := newFixtureLibrary(t)
	ctx := context.Background()

	rel, err := lib.BookFilePath(ctx, 1, "epub")
	if err != nil {
		t.Fatal(err)
	}
	if rel != "Ada Lovelace/Alpha (1)/Alpha - Ada Lovelace.epub" {
		t.Errorf("epub rel = %q", rel)
	}

	// case-insensitive on format
	if _, err := lib.BookFilePath(ctx, 1, "PDF"); err != nil {
		t.Errorf("PDF lookup: %v", err)
	}

	if _, err := lib.BookFilePath(ctx, 1, "mobi"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing format: err = %v, want ErrNotFound", err)
	}
	if _, err := lib.BookFilePath(ctx, 9999, "epub"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing book: err = %v, want ErrNotFound", err)
	}
}

func TestOpenRejectsNonLibrary(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Errorf("Open of empty dir should fail")
	}
	if _, err := Open(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Errorf("Open of nonexistent dir should fail")
	}
}
