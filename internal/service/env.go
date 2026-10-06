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
	if path, ok := lookPath(name, env["PATH"]); ok {
		return path, nil
	}
	return "", errNoGH
}

// lookPath finds the executable name in an absolute dir of the PATH list path.
func lookPath(name, path string) (string, bool) {
	for _, dir := range filepath.SplitList(path) {
		file := filepath.Join(dir, name)
		if filepath.IsAbs(file) && isExecutable(file) {
			return file, true
		}
	}
	return "", false
}

var errNoGH = errors.New("gh is neither at LG_GH nor on PATH; install gh or set LG_GH to its path")

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// baked are the variables a unit keeps from install time when they are set.
// The XDG dirs and GH_CONFIG_DIR locate the config, store and gh's login.
var baked = []string{
	"LG_HOME", "LG_CONFIG", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "GH_CONFIG_DIR", "SSL_CERT_FILE",
	"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
}

// Env gives the environment a unit bakes in: LG_GH as gh, and those of
// baked that env sets. It never holds a token. A service runs in another
// dir, so Env makes a relative path absolute, or drops a relative XDG dir,
// which the XDG spec says to ignore.
func Env(env map[string]string, gh string) (map[string]string, error) {
	out := map[string]string{"LG_GH": gh}
	for _, k := range baked {
		v := env[k]
		switch {
		case v == "":
			continue
		case k == "SSL_CERT_FILE" || k == "GH_CONFIG_DIR":
			abs, err := filepath.Abs(v)
			if err != nil {
				return nil, err
			}
			v = abs
		case strings.HasPrefix(k, "XDG_") && !filepath.IsAbs(v):
			continue
		}
		out[k] = v
	}
	return out, nil
}

// Executable gives the path lg was run by, as arg0 and the PATH list path
// name it, when that is self. It keeps a symlink, so a unit follows an
// upgrade that repoints it. Otherwise it gives self.
func Executable(arg0, path, self string) string {
	name := arg0
	if !strings.Contains(arg0, "/") {
		name, _ = lookPath(arg0, path)
	}
	abs, err := filepath.Abs(name)
	if err != nil || !sameFile(abs, self) {
		return self
	}
	return abs
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
