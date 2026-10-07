package harness

import (
	"os"
	"strings"
)

var droppedPrefixes = []string{"LG_", "XDG_", "GH_", "GITHUB_"}

var droppedNames = map[string]bool{
	"HOME":                true,
	"CLAUDE_CONFIG_DIR":   true,
	"RIPGREP_CONFIG_PATH": true,
	"HTTPS_PROXY":         true,
	"HTTP_PROXY":          true,
	"NO_PROXY":            true,
	"ALL_PROXY":           true,
	"SSL_CERT_FILE":       true,
}

// Scrub drops variables that would leak the caller's lg, XDG, GitHub, home,
// Claude config, ripgrep config, proxy or CA bundle settings into a spec, and
// puts binDir first on PATH.
func Scrub(environ []string, binDir string) map[string]string {
	vars := map[string]string{"PATH": binDir}
	for _, kv := range environ {
		key, value, _ := strings.Cut(kv, "=")
		switch {
		case key == "PATH":
			vars["PATH"] += string(os.PathListSeparator) + value
		case !dropped(key):
			vars[key] = value
		}
	}
	return vars
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

// liveKept are the dropped variables a live spec needs: the egress proxy,
// its CA bundle, gh's token and config, and HOME for gh's keyring.
var liveKept = map[string]bool{
	"HTTPS_PROXY":   true,
	"HTTP_PROXY":    true,
	"NO_PROXY":      true,
	"SSL_CERT_FILE": true,
	"GH_TOKEN":      true,
	"GITHUB_TOKEN":  true,
	"GH_CONFIG_DIR": true,
	"HOME":          true,
}

// ScrubLive is Scrub for specs that reach github.com through the real gh.
func ScrubLive(environ []string, binDir string) map[string]string {
	vars := Scrub(environ, binDir)
	for _, kv := range environ {
		key, value, _ := strings.Cut(kv, "=")
		if liveKept[strings.ToUpper(key)] {
			vars[key] = value
		}
	}
	return vars
}
