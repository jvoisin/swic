// Package calibre provides read-only access to a Calibre library.
package calibre

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("calibre: not found")

// Library is a handle to a Calibre library directory.
type Library struct {
	root *os.Root
	fsys fs.FS
	db   *sql.DB
}

// Open opens the Calibre library at root for read-only access.
// root must be a directory containing a metadata.db file. All file accesses
// after Open are constrained to the directory tree by the kernel via *os.Root.
func Open(root string) (*Library, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("calibre: open root: %w", err)
	}
	if _, err := r.Stat("metadata.db"); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("calibre: metadata.db not found in %s", root)
	}

	dbPath := filepath.Join(r.Name(), "metadata.db")
	dsn := "file:" + url.PathEscape(dbPath) + "?mode=ro&_pragma=query_only(true)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("calibre: open db: %w", err)
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		_ = r.Close()
		return nil, fmt.Errorf("calibre: ping db: %w", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM books`).Scan(&n); err != nil {
		_ = db.Close()
		_ = r.Close()
		return nil, fmt.Errorf("calibre: schema check failed (is this a Calibre library?): %w", err)
	}
	return &Library{root: r, fsys: r.FS(), db: db}, nil
}

// BookCount returns the total number of books in the library.
func (l *Library) BookCount() int {
	var n int
	_ = l.db.QueryRow(`SELECT count(*) FROM books`).Scan(&n)
	return n
}

// Close releases resources held by the library.
func (l *Library) Close() error {
	dbErr := l.db.Close()
	rootErr := l.root.Close()
	if dbErr != nil {
		return dbErr
	}
	return rootErr
}

// Root returns the absolute path to the library directory.
func (l *Library) Root() string { return l.root.Name() }

// FS returns a traversal-safe fs.FS rooted at the library directory.
// It is suitable for passing to http.ServeFileFS.
func (l *Library) FS() fs.FS { return l.fsys }
