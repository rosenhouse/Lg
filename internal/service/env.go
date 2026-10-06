package service

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// FindGH gives the absolute path of the gh a daemon started with env would
// run: LG_GH, looked up on PATH unless it holds a slash, else gh on PATH.
func FindGH(env map[string]string) (string, error) {
	name := cmp.Or(env["LG_GH"], "gh")
	if strings.Contains(name, "/") {
		if !isExecutable(name) {
			return "", errNoGH
		}
		return filepath.Abs(name)
	}
	for _, dir := range filepath.SplitList(env["PATH"]) {
		path := filepath.Join(dir, name)
		if filepath.IsAbs(path) && isExecutable(path) {
			return path, nil
		}
	}
	return "", errNoGH
}

var errNoGH = errors.New("gh is neither at LG_GH nor on PATH; install gh or set LG_GH to its path")

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// baked are the variables a unit keeps from install time when they are set.
var baked = []string{
	"LG_HOME", "LG_CONFIG", "SSL_CERT_FILE",
	"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
}

// Env gives the environment a unit bakes in: LG_GH as gh, and those of
// baked that env sets. It never holds a token.
func Env(env map[string]string, gh string) map[string]string {
	out := map[string]string{"LG_GH": gh}
	for _, k := range baked {
		if env[k] != "" {
			out[k] = env[k]
		}
	}
	return out
}
