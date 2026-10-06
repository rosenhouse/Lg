package service

import (
	"path/filepath"
	"regexp"
)

// goWorkDir matches the temp dir go run builds in, as os.MkdirTemp names it.
var goWorkDir = regexp.MustCompile(`^go-build[0-9]+$`)

// BuiltByGoRun reports whether exe lies under a go run build dir, which go
// removes when the run ends.
func BuiltByGoRun(exe string) bool {
	for dir := filepath.Dir(exe); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if goWorkDir.MatchString(filepath.Base(dir)) {
			return true
		}
	}
	return false
}
