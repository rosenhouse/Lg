package service

import (
	"path/filepath"
	"regexp"
)

var (
	// goWorkDir matches the temp dir go run builds in, as os.MkdirTemp names it.
	goWorkDir = regexp.MustCompile(`^go-build[0-9]+$`)
	// goCacheExeDir matches the GOCACHE dir go run caches a binary in.
	goCacheExeDir = regexp.MustCompile(`^[0-9a-f]{64}-d$`)
)

// BuiltByGoRun reports whether exe lies under a go run build dir or in
// GOCACHE, which go removes or trims.
func BuiltByGoRun(exe string) bool {
	if goCacheExeDir.MatchString(filepath.Base(filepath.Dir(exe))) {
		return true
	}
	for dir := filepath.Dir(exe); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if goWorkDir.MatchString(filepath.Base(dir)) {
			return true
		}
	}
	return false
}
