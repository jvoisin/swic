package calibre

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
)

// CoverPath returns the cover image's path relative to the library root,
// suitable for passing to http.ServeFileFS together with l.FS().
// Returns ErrNotFound if the book has no cover or does not exist.
func (l *Library) CoverPath(ctx context.Context, id int64) (string, error) {
	var p string
	var hasCover int
	err := l.db.QueryRowContext(ctx,
		`SELECT path, has_cover FROM books WHERE id = ?`, id).Scan(&p, &hasCover)
	if errors.Is(err, sql.ErrNoRows) || hasCover == 0 {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("calibre: cover lookup: %w", err)
	}
	return path.Join(p, "cover.jpg"), nil
}

// BookFilePath returns the book file's path relative to the library root,
// suitable for http.ServeFileFS with l.FS(). The download filename is
// path.Base(rel). Format is matched case-insensitively against data.format.
func (l *Library) BookFilePath(ctx context.Context, id int64, format string) (string, error) {
	var bookPath, name, fmtName string
	err := l.db.QueryRowContext(ctx, `
		SELECT books.path, data.name, data.format
		FROM data
		JOIN books ON books.id = data.book
		WHERE data.book = ? AND UPPER(data.format) = UPPER(?)
		LIMIT 1`, id, format).Scan(&bookPath, &name, &fmtName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("calibre: file lookup: %w", err)
	}
	return path.Join(bookPath, name+"."+strings.ToLower(fmtName)), nil
}
