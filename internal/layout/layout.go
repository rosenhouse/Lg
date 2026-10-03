// Package layout names the directories of the data tree.
package layout

import "strings"

const maxSlug = 60

// Slug keeps the bytes [A-Za-z0-9.-] of name, turns each run of other bytes
// into one -, trims - and . from both ends, and truncates to 60 bytes.
func Slug(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !allowed(c) {
			c = '-'
		}
		if c == '-' && strings.HasSuffix(b.String(), "-") {
			continue
		}
		b.WriteByte(c)
	}
	slug := strings.Trim(b.String(), "-.")
	if len(slug) > maxSlug {
		slug = strings.TrimRight(slug[:maxSlug], "-.")
	}
	if slug == "" {
		return "none"
	}
	return slug
}

func allowed(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '.' || c == '-'
}
