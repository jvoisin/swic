package calibre

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// SortOrder controls how ListBooks orders results.
type SortOrder string

const (
	SortByTitle    SortOrder = "title"
	SortByAuthor   SortOrder = "author"
	SortByDate     SortOrder = "date"     // most recently added first
	SortByLastRead SortOrder = "lastread" // most recently read first
)

// SearchField selects which column(s) the search string is matched against.
type SearchField string

const (
	SearchAny       SearchField = "" // title OR author OR series (default)
	SearchTitle     SearchField = "title"
	SearchAuthor    SearchField = "author"
	SearchSeries    SearchField = "series"
	SearchTag       SearchField = "tag"
	SearchPublisher SearchField = "publisher"
)

// ListQuery is the parameter object for ListBooks.
type ListQuery struct {
	Limit  int
	Offset int
	Sort   SortOrder
	// Search is a case-insensitive substring matched against the field
	// selected by SearchIn (or title/author/series when unset).
	Search   string
	SearchIn SearchField
}

// Format describes one downloadable file attached to a book.
type Format struct {
	Name      string // file basename without extension (data.name)
	Format    string // e.g. "epub", "pdf" (data.format)
	SizeBytes int64
}

// ReadingProgress holds the last known reading position for a book.
type ReadingProgress struct {
	Fraction float64   // 0.0 to 1.0
	LastRead time.Time // when last read
}

// BookSummary is the compact view used for listings.
type BookSummary struct {
	ID          int64
	Title       string
	Authors     []string
	SeriesName  string
	SeriesIndex float64
	HasCover    bool
	Timestamp   time.Time
	Progress    *ReadingProgress // nil if never read
}

// Identifier is a typed external identifier (isbn, goodreads, etc.).
type Identifier struct {
	Type  string
	Value string
}

// Book is the full view used on the detail page.
type Book struct {
	BookSummary
	PubDate     time.Time
	Tags        []string
	Languages   []string
	Publisher   string
	Identifiers []Identifier
	Description string // plain text, sanitized from comments.text HTML
	Formats     []Format
}

var orderByClause = map[SortOrder]string{
	SortByAuthor:   "author_sort COLLATE NOCASE ASC, sort COLLATE NOCASE ASC",
	SortByDate:     "timestamp DESC",
	SortByTitle:    "sort COLLATE NOCASE ASC",
	SortByLastRead: "last_read_epoch DESC, timestamp DESC",
}

// ListBooks returns a page of book summaries plus the total book count.
// Note: when offset is past the last matching row the returned total is 0
// (the count is computed via a window function over the result rows).
func (l *Library) ListBooks(ctx context.Context, q ListQuery) ([]BookSummary, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	q.Offset = max(q.Offset, 0)
	orderBy, ok := orderByClause[q.Sort]
	if !ok {
		orderBy = orderByClause[SortByDate]
	}
	joins, where, whereArgs := searchClause(q.SearchIn, q.Search)

	query := `SELECT id, title, has_cover, timestamp, series_index, series_name,
	                 COUNT(*) OVER () AS total
	          FROM (
	              SELECT DISTINCT books.id AS id, books.title AS title,
	                     books.has_cover AS has_cover, books.timestamp AS timestamp,
	                     books.sort AS sort, books.author_sort AS author_sort,
	                     COALESCE(books.series_index, 1.0) AS series_index,
	                     COALESCE(series.name, '') AS series_name,
	                     COALESCE(lrp.epoch, 0) AS last_read_epoch
	              FROM books
	              LEFT JOIN books_series_link ON books_series_link.book = books.id
	              LEFT JOIN series ON series.id = books_series_link.series
	              LEFT JOIN (SELECT book, MAX(epoch) AS epoch FROM last_read_positions GROUP BY book) lrp ON lrp.book = books.id` +
		joins +
		where + `
	          )
	          ORDER BY ` + orderBy + ` LIMIT ? OFFSET ?`

	rows, err := l.db.QueryContext(ctx, query,
		slices.Concat(whereArgs, []any{q.Limit, q.Offset})...)
	if err != nil {
		return nil, 0, fmt.Errorf("calibre: list books: %w", err)
	}
	defer rows.Close()

	out := make([]BookSummary, 0, q.Limit)
	var total int
	for rows.Next() {
		var (
			b        BookSummary
			hasCover int
			ts       string
		)
		if err := rows.Scan(&b.ID, &b.Title, &hasCover, &ts,
			&b.SeriesIndex, &b.SeriesName, &total); err != nil {
			return nil, 0, fmt.Errorf("calibre: scan book: %w", err)
		}
		b.HasCover = hasCover != 0
		b.Timestamp = parseCalibreTime(ts)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	ids := make([]int64, len(out))
	for i, b := range out {
		ids[i] = b.ID
	}
	authors, err := l.authorsForBooks(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	progress, err := l.progressForBooks(ctx, ids)
	if err != nil {
		// Table may not exist in older Calibre libraries; ignore silently.
		progress = nil
	}
	for i := range out {
		out[i].Authors = authors[out[i].ID]
		if p, ok := progress[out[i].ID]; ok {
			out[i].Progress = &p
		}
	}
	return out, total, nil
}

// GetBook returns the full record for a single book.
func (l *Library) GetBook(ctx context.Context, id int64) (*Book, error) {
	const q = `
		SELECT books.id, books.title, books.has_cover, books.timestamp,
		       COALESCE(books.pubdate, ''),
		       COALESCE(books.series_index, 1.0),
		       COALESCE(series.name, ''),
		       COALESCE(comments.text, ''),
		       COALESCE(publishers.name, '')
		FROM books
		LEFT JOIN books_series_link ON books_series_link.book = books.id
		LEFT JOIN series ON series.id = books_series_link.series
		LEFT JOIN comments ON comments.book = books.id
		LEFT JOIN books_publishers_link ON books_publishers_link.book = books.id
		LEFT JOIN publishers ON publishers.id = books_publishers_link.publisher
		WHERE books.id = ?`
	var b Book
	var hasCover int
	var ts, pub, comments string
	err := l.db.QueryRowContext(ctx, q, id).Scan(
		&b.ID, &b.Title, &hasCover, &ts, &pub,
		&b.SeriesIndex, &b.SeriesName, &comments, &b.Publisher,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("calibre: get book: %w", err)
	}
	b.HasCover = hasCover != 0
	b.Timestamp = parseCalibreTime(ts)
	b.PubDate = parseCalibreTime(pub)
	b.Description = stripHTML(comments)

	if b.Authors, err = queryAll(ctx, l.db, `
		SELECT authors.name FROM authors
		JOIN books_authors_link ON books_authors_link.author = authors.id
		WHERE books_authors_link.book = ?
		ORDER BY books_authors_link.id`, scanString, b.ID); err != nil {
		return nil, fmt.Errorf("calibre: authors: %w", err)
	}

	if b.Tags, err = queryAll(ctx, l.db, `
		SELECT tags.name FROM tags
		JOIN books_tags_link ON books_tags_link.tag = tags.id
		WHERE books_tags_link.book = ?
		ORDER BY tags.name COLLATE NOCASE`, scanString, b.ID); err != nil {
		return nil, fmt.Errorf("calibre: tags: %w", err)
	}
	if b.Languages, err = queryAll(ctx, l.db, `
		SELECT languages.lang_code FROM languages
		JOIN books_languages_link ON books_languages_link.lang_code = languages.id
		WHERE books_languages_link.book = ?
		ORDER BY books_languages_link.item_order`, scanString, b.ID); err != nil {
		return nil, fmt.Errorf("calibre: languages: %w", err)
	}
	if b.Identifiers, err = queryAll(ctx, l.db,
		`SELECT type, val FROM identifiers WHERE book = ? ORDER BY type`,
		scanIdentifier, b.ID); err != nil {
		return nil, fmt.Errorf("calibre: identifiers: %w", err)
	}
	if b.Formats, err = queryAll(ctx, l.db,
		`SELECT name, format, COALESCE(uncompressed_size, 0) FROM data WHERE book = ? ORDER BY format`,
		scanFormat, b.ID); err != nil {
		return nil, fmt.Errorf("calibre: formats: %w", err)
	}
	progress, _ := l.progressForBooks(ctx, []int64{b.ID})
	if p, ok := progress[b.ID]; ok {
		b.Progress = &p
	}
	return &b, nil
}

// queryAll runs q with args and returns the slice of values produced by scan.
func queryAll[T any](ctx context.Context, db *sql.DB, q string, scan func(*sql.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanString(r *sql.Rows) (string, error) {
	var s string
	return s, r.Scan(&s)
}

func scanIdentifier(r *sql.Rows) (Identifier, error) {
	var i Identifier
	return i, r.Scan(&i.Type, &i.Value)
}

func scanFormat(r *sql.Rows) (Format, error) {
	var f Format
	return f, r.Scan(&f.Name, &f.Format, &f.SizeBytes)
}

func (l *Library) authorsForBooks(ctx context.Context, ids []int64) (map[int64][]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Join(slices.Repeat([]string{"?"}, len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT books_authors_link.book, authors.name
		FROM authors
		JOIN books_authors_link ON books_authors_link.author = authors.id
		WHERE books_authors_link.book IN (`+placeholders+`)
		ORDER BY books_authors_link.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("calibre: authors: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][]string, len(ids))
	for rows.Next() {
		var book int64
		var name string
		if err := rows.Scan(&book, &name); err != nil {
			return nil, err
		}
		out[book] = append(out[book], name)
	}
	return out, rows.Err()
}

func (l *Library) progressForBooks(ctx context.Context, ids []int64) (map[int64]ReadingProgress, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Join(slices.Repeat([]string{"?"}, len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	// Pick the most recent reading position per book.
	rows, err := l.db.QueryContext(ctx, `
		SELECT book, MAX(pos_frac), MAX(epoch)
		FROM last_read_positions
		WHERE book IN (`+placeholders+`)
		GROUP BY book`, args...)
	if err != nil {
		return nil, fmt.Errorf("calibre: reading progress: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]ReadingProgress, len(ids))
	for rows.Next() {
		var book int64
		var frac float64
		var epoch float64
		if err := rows.Scan(&book, &frac, &epoch); err != nil {
			return nil, err
		}
		out[book] = ReadingProgress{
			Fraction: frac,
			LastRead: time.Unix(int64(epoch), 0),
		}
	}
	return out, rows.Err()
}

// searchSpec describes which JOINs and WHERE fragment participate in a search.
type searchSpec struct {
	joins   string
	where   string // " WHERE (col1 LIKE ? ... OR col2 LIKE ? ...)"
	numArgs int
}

const (
	authorSearchJoin = ` LEFT JOIN books_authors_link ON books_authors_link.book = books.id` +
		` LEFT JOIN authors ON authors.id = books_authors_link.author`
	tagSearchJoin = ` LEFT JOIN books_tags_link ON books_tags_link.book = books.id` +
		` LEFT JOIN tags ON tags.id = books_tags_link.tag`
	publisherSearchJoin = ` LEFT JOIN books_publishers_link ON books_publishers_link.book = books.id` +
		` LEFT JOIN publishers ON publishers.id = books_publishers_link.publisher`
)

var searchSpecs = func() map[SearchField]searchSpec {
	specs := map[SearchField]struct {
		joins string
		cols  []string
	}{
		SearchTitle:     {"", []string{"books.title"}},
		SearchAuthor:    {authorSearchJoin, []string{"authors.name"}},
		SearchSeries:    {"", []string{"series.name"}},
		SearchTag:       {tagSearchJoin, []string{"tags.name"}},
		SearchPublisher: {publisherSearchJoin, []string{"publishers.name"}},
		SearchAny:       {authorSearchJoin, []string{"books.title", "series.name", "authors.name"}},
	}
	const like = ` LIKE ? ESCAPE '\' COLLATE NOCASE`
	out := make(map[SearchField]searchSpec, len(specs))
	for k, s := range specs {
		parts := make([]string, len(s.cols))
		for i, c := range s.cols {
			parts[i] = c + like
		}
		out[k] = searchSpec{
			joins:   s.joins,
			where:   " WHERE (" + strings.Join(parts, " OR ") + ")",
			numArgs: len(s.cols),
		}
	}
	return out
}()

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// searchClause returns extra JOINs, a WHERE fragment (starting with " WHERE ...")
// and bind args for an optional case-insensitive substring search.
// An unknown field falls back to title OR author OR series.
func searchClause(field SearchField, search string) (joins, where string, args []any) {
	s := strings.TrimSpace(search)
	if s == "" {
		return "", "", nil
	}
	spec, ok := searchSpecs[field]
	if !ok {
		spec = searchSpecs[SearchAny]
	}
	pat := "%" + likeEscaper.Replace(s) + "%"
	args = make([]any, spec.numArgs)
	for i := range args {
		args[i] = pat
	}
	return spec.joins, spec.where, args
}

// calibreTimeLayouts are tried in order; Calibre normally stores
// `2006-01-02 15:04:05.000000+00:00`.
var calibreTimeLayouts = []string{
	"2006-01-02 15:04:05.000000-07:00",
	"2006-01-02 15:04:05-07:00",
	time.RFC3339,
}

// parseCalibreTime parses Calibre's stored timestamps; returns zero on failure.
func parseCalibreTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range calibreTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
