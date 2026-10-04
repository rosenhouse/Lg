// Package layout names the directories of the data tree.
package layout

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/model"
)

const maxSlug = 60

// Slug maps each byte outside [A-Za-z0-9.-] to -, collapses runs of -, trims
// - and . from both ends, truncates to 60 bytes, and gives none when nothing is left.
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

var ownerName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// IsRepo reports whether s is owner/name, safe to use as a path.
func IsRepo(s string) bool {
	return ownerName.MatchString(s) && !slices.ContainsFunc(strings.Split(s, "/"), isDots)
}

func isDots(s string) bool { return s == "." || s == ".." }

// RepoDir is <data>/<host>/<owner>/<repo>, with the host lowercased and the
// repo's full name spelled as GitHub returns it.
func RepoDir(data, host, fullName string) string {
	return filepath.Join(data, strings.ToLower(host), fullName)
}

// RunDir is runs/<UTC date of created_at>/<id>_<workflow slug>_<branch slug>.
func RunDir(repoDir string, run model.Run) string {
	workflow := run.Name
	if workflow == "" {
		base := path.Base(run.Path)
		workflow = strings.TrimSuffix(base, path.Ext(base))
	}
	name := fmt.Sprintf("%d_%s_%s", run.ID, Slug(workflow), Slug(run.HeadBranch))
	return filepath.Join(repoDir, "runs", run.CreatedAt.UTC().Format(time.DateOnly), name)
}

func AttemptDir(runDir string, attempt int) string {
	return filepath.Join(runDir, fmt.Sprintf("attempt-%d", attempt))
}

func JobDir(attemptDir string, id int64, name string) string {
	return filepath.Join(attemptDir, "jobs", fmt.Sprintf("%d_%s", id, Slug(name)))
}

func ArtifactDir(runDir string, id int64, name string) string {
	return filepath.Join(runDir, "artifacts", fmt.Sprintf("%d_%s", id, Slug(name)))
}

// AttemptNumber gives n for the base name of AttemptDir(_, n).
func AttemptNumber(name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, "attempt-")
	n, err := strconv.Atoi(digits)
	if !ok || err != nil || strconv.Itoa(n) != digits {
		return 0, false
	}
	return n, true
}
