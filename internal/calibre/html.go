package calibre

import (
	"html"
	"regexp"
	"strings"
)

var (
	// Drop <script>...</script> and <style>...</style> blocks, content included.
	scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</\s*script\s*>`)
	styleRe  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</\s*style\s*>`)
	// Block-level tags whose presence should produce a line break.
	blockTagRe = regexp.MustCompile(`(?i)<\s*/?(p|div|li|tr|br)\b[^>]*/?>`)
	tagRe      = regexp.MustCompile(`<[^>]*>`)
	manyBlanks = regexp.MustCompile(`\n{3,}`)
)

// stripHTML returns a plain-text version of the input HTML.
// The result is rendered through html/template, which auto-escapes it.
func stripHTML(in string) string {
	if in == "" {
		return ""
	}
	s := scriptRe.ReplaceAllString(in, "")
	s = styleRe.ReplaceAllString(s, "")
	s = blockTagRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = manyBlanks.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
