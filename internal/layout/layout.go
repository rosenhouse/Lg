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

// DirID gives id for the base name of RunDir, JobDir or ArtifactDir of id.
func DirID(name string) (int64, bool) {
	digits, _, ok := strings.Cut(name, "_")
	id, err := strconv.ParseInt(digits, 10, 64)
	if !ok || err != nil || strconv.FormatInt(id, 10) != digits {
		return 0, false
	}
	return id, true
}

// Location is what a path relative to data/ names. Each dir is relative to
// data/, and empty when the path is not in one.
type Location struct {
	Host, Repo  string
	RunID       int64
	RunDir      string
	Attempt     int
	AttemptDir  string
	JobID       int64
	JobDir      string
	ArtifactID  int64
	ArtifactDir string
	// File is the rest of the path, below the deepest of those dirs.
	File string
}

// Parse decodes a path relative to data/ that is a run dir or below one.
func Parse(path string) (Location, error) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	if filepath.IsAbs(path) || len(parts) < 6 || isDots(parts[0]) || !IsRepo(parts[1]+"/"+parts[2]) || parts[3] != "runs" {
		return Location{}, fmt.Errorf("%s is not in a run dir", path)
	}
	runID, ok := DirID(parts[5])
	if _, err := time.Parse(time.DateOnly, parts[4]); err != nil || !ok {
		return Location{}, fmt.Errorf("%s is not in a run dir", path)
	}
	loc := Location{Host: parts[0], Repo: parts[1] + "/" + parts[2], RunID: runID, RunDir: filepath.Join(parts[:6]...)}
	rest := parts[6:]
	switch {
	case len(rest) == 0:
	case rest[0] == "artifacts" && len(rest) > 1:
		if loc.ArtifactID, ok = DirID(rest[1]); !ok {
			return Location{}, fmt.Errorf("%s: %s is not an artifact dir", path, rest[1])
		}
		loc.ArtifactDir, rest = filepath.Join(parts[:8]...), rest[2:]
	case rest[0] != "artifacts":
		if loc.Attempt, ok = AttemptNumber(rest[0]); !ok {
			return Location{}, fmt.Errorf("%s: %s is not an attempt dir", path, rest[0])
		}
		loc.AttemptDir, rest = filepath.Join(parts[:7]...), rest[1:]
		if len(rest) > 1 && rest[0] == "jobs" {
			if loc.JobID, ok = DirID(rest[1]); !ok {
				return Location{}, fmt.Errorf("%s: %s is not a job dir", path, rest[1])
			}
			loc.JobDir, rest = filepath.Join(parts[:9]...), rest[2:]
		}
	}
	loc.File = filepath.Join(rest...)
	return loc, nil
}

// Hit is a line rg or grep printed: a path, and the line number and text that
// follow it when they do.
type Hit struct {
	Path string
	Line int
	Text string
}

// ParseHit splits hit at the longest prefix that exists, since a path may
// hold colons, and gives false when no prefix exists. It reads a match as
// path:line:text, path:line or path:text, and a context line as path-line-text.
func ParseHit(hit string, exists func(path string) bool) (Hit, bool) {
	for end := len(hit); end > 0; end = strings.LastIndexAny(hit[:end], ":-") {
		if !exists(hit[:end]) {
			continue
		}
		h := Hit{Path: hit[:end]}
		if end == len(hit) {
			return h, true
		}
		sep, rest := hit[end], hit[end+1:]
		digits, text, found := strings.Cut(rest, string(sep))
		n, err := strconv.Atoi(digits)
		isLine := err == nil && n > 0 && strconv.Itoa(n) == digits
		switch {
		case isLine && (found || sep == ':'):
			h.Line, h.Text = n, text
		case sep == ':':
			h.Text = rest
		default:
			continue
		}
		return h, true
	}
	return Hit{}, false
}
