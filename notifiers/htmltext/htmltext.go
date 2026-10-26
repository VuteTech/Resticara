// Package htmltext converts the HTML notification templates to plain text.
package htmltext

import (
	"html"
	"regexp"
	"strings"
)

var (
	lineBreak = regexp.MustCompile(`(?i)<br\s*/?>|</h[1-6]>|</p>`)
	tag       = regexp.MustCompile(`<[^>]*>`)
)

// PlainText strips HTML tags, turning line-breaking elements into newlines, and unescapes entities.
func PlainText(s string) string {
	s = lineBreak.ReplaceAllString(s, "\n")
	s = tag.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}
