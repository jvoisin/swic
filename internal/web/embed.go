// Package web implements the HTTP interface for swic.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"strings"
	"time"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

func parseTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"humanSize": humanSize,
		"fmtDate":   fmtDate,
		"lower":     strings.ToLower,
		"searchURL": searchURL,
	}
	return template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.gohtml")
}

func staticSubFS() (fs.FS, error) {
	return fs.Sub(staticFS, "static")
}

// --- template helpers ---

// searchURL builds a /books URL that searches for value in the given field.
func searchURL(field, value string) string {
	v := url.Values{}
	v.Set("q", value)
	if field != "" {
		v.Set("in", field)
	}
	return "/books?" + v.Encode()
}

// humanSize renders a byte count using binary (IEC) units.
func humanSize(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// fmtDate renders a date as YYYY-MM-DD; the zero time renders as "".
func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
