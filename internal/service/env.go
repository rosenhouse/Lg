package service

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// systemDirs are the dirs a unit's PATH holds after gh's.
var systemDirs = map[string][]string{
	"linux":  {"/usr/local/bin", "/usr/bin", "/bin"},
	"darwin": {"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"},
}

// Env gives the environment a unit on goos bakes in: PATH with gh's dir
// first, LG_GH as gh only when env sets it, and those of baked that env
// sets. A set LG_GH permits a loopback api_url, so baking it unasked would
// let a real token reach a local port. Env never holds a token. A service
// runs in another dir, so Env makes a relative path absolute, or drops a
// relative XDG dir, which the XDG spec says to ignore.
func Env(goos string, env map[string]string, gh string) (map[string]string, error) {
	path := []string{filepath.Dir(gh)}
	for _, dir := range systemDirs[goos] {
		if !slices.Contains(path, dir) {
			path = append(path, dir)
		}
	}
	out := map[string]string{"PATH": strings.Join(path, string(os.PathListSeparator))}
	if env["LG_GH"] != "" {
		out["LG_GH"] = gh
	}
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

// Executable gives arg0 made absolute, looked up on path when bare, if it
// names self. Keeping a symlink lets the unit follow an upgrade that
// repoints it. Otherwise it gives self.
func Executable(arg0, path, self string) string {
	name := arg0
	if !strings.Contains(arg0, "/") {
		var ok bool
		if name, ok = lookPath(arg0, path); !ok {
			return self
		}
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
