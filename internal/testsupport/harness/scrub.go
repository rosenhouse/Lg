package harness

import (
	"os"
	"strings"
)

var droppedPrefixes = []string{"LG_", "XDG_", "GH_", "GITHUB_"}

var droppedNames = map[string]bool{
	"HOME":        true,
	"HTTPS_PROXY": true,
	"HTTP_PROXY":  true,
	"NO_PROXY":    true,
	"ALL_PROXY":   true,
}

// Scrub drops variables that would leak the caller's lg, XDG, GitHub, home or
// proxy settings into a spec, and puts binDir first on PATH.
func Scrub(environ []string, binDir string) []string {
	path := binDir
	var kept []string
	for _, kv := range environ {
		key, value, _ := strings.Cut(kv, "=")
		switch {
		case key == "PATH":
			path += string(os.PathListSeparator) + value
		case !dropped(key):
			kept = append(kept, kv)
		}
	}
	return append(kept, "PATH="+path)
}

func dropped(key string) bool {
	if droppedNames[strings.ToUpper(key)] {
		return true
	}
	for _, p := range droppedPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}
