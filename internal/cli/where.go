package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/tombstone"
)

type whereCmd struct {
	Hits []string `arg:"" optional:"" name:"path|hit" help:"A path in the store, absolute, or relative to the store, a repo dir in it, or the working directory; or a line rg or grep printed. Without any, lines are read from stdin."`
}

func (whereCmd) Help() string {
	return "Prints one JSON object per input. It reads files only, never lg.db."
}

func (w whereCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	out := json.NewEncoder(deps.Stdout)
	out.SetEscapeHTML(false)
	finder := newPlaceFinder(roots.Data)
	defer finder.lines.close()
	var failed error
	err = eachInput(w.Hits, deps.Stdin, func(hit string) error {
		p, err := finder.find(hit)
		if err != nil {
			printError(deps.Stderr, err)
			failed = warned{err}
			return nil
		}
		return out.Encode(p)
	})
	if err != nil {
		return err
	}
	return failed
}

// eachInput calls f with each of hits, or else with each line of stdin that
// is neither blank nor rg's -- separator.
func eachInput(hits []string, stdin io.Reader, f func(string) error) error {
	if len(hits) > 0 {
		for _, hit := range hits {
			if err := f(hit); err != nil {
				return err
			}
		}
		return nil
	}
	lines := bufio.NewReader(stdin)
	for {
		line, err := lines.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.TrimSpace(line) != "" && line != "--" {
			if err := f(line); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// place is what lg where prints of a path or hit.
type place struct {
	Path    string `json:"path"`
	Host    string `json:"host"`
	Repo    string `json:"repo"`
	RunID   int64  `json:"run_id"`
	Attempt int    `json:"attempt,omitempty"`
	*jobPlace
	*artifactPlace
	Workflow  string    `json:"workflow"`
	Branch    string    `json:"branch"`
	SHA       string    `json:"sha"`
	Event     string    `json:"event"`
	PRs       []int     `json:"prs"`
	CreatedAt time.Time `json:"created_at"`
	*tombstonePlace
	Line    int     `json:"line,omitempty"`
	Text    string  `json:"text,omitempty"`
	HTMLURL *string `json:"html_url"`
}

type jobPlace struct {
	JobID          int64  `json:"job_id"`
	Job            string `json:"job"`
	JobConclusion  string `json:"job_conclusion"`
	CarriedForward bool   `json:"carried_forward"`
	OriginalJobID  int64  `json:"original_job_id,omitempty"`
	OriginalLog    string `json:"original_log,omitempty"`
}

type artifactPlace struct {
	ArtifactID        int64             `json:"artifact_id"`
	Artifact          string            `json:"artifact"`
	AttributedAttempt *int              `json:"attributed_attempt"`
	Attribution       model.Attribution `json:"attribution"`
}

type tombstonePlace struct {
	Reason     tombstone.Reason `json:"reason"`
	HTTPStatus *int             `json:"http_status"`
	Message    string           `json:"message"`
}

// runFacts are a run's rows, as index.IndexRun derives them from its files.
type runFacts struct {
	rows index.Rows
	err  error
}

// placeFinder decodes hits in the store at data, reading each run once
// unless its files change.
type placeFinder struct {
	data string
	// real is data with symlinks resolved.
	real  string
	repos []string
	runs  map[string]runFacts
	lines *lineReader
}

func newPlaceFinder(data string) placeFinder {
	// A missing data/ holds no path, so its real path need not be known.
	real, _ := filepath.EvalSymlinks(data)
	return placeFinder{data: data, real: real, repos: repoDirs(data), runs: map[string]runFacts{}, lines: newLineReader(openFile)}
}

// repoDirs gives each data/<host>/<owner>/<repo>.
func repoDirs(data string) []string {
	dirs := []string{data}
	for range 3 {
		var below []string
		for _, dir := range dirs {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.IsDir() {
					below = append(below, filepath.Join(dir, e.Name()))
				}
			}
		}
		dirs = below
	}
	return dirs
}

var leadingLine = regexp.MustCompile(`^[0-9]+:`)

func (f placeFinder) find(hit string) (place, error) {
	var path string
	h, ok := layout.ParseHit(hit, func(p string) (found bool) {
		path, found = f.existing(p)
		return found
	})
	if !ok {
		if leadingLine.MatchString(hit) {
			return place{}, fmt.Errorf("%q names no file; run rg with -H to print file names", hit)
		}
		return place{}, fmt.Errorf("%q names no file", hit)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return place{}, err
	}
	rel, err := filepath.Rel(f.real, real)
	if err != nil || !filepath.IsLocal(rel) {
		return place{}, fmt.Errorf("%s is outside the store %s", path, f.data)
	}
	loc, err := layout.Parse(rel)
	if err != nil {
		return place{}, fmt.Errorf("%s: %w", path, err)
	}
	if h.Line > 0 {
		var holds bool
		if h.Text, holds = f.holding(path, h.Line, h.Text); !holds {
			h.Line, h.Text = 0, hit[len(h.Path)+1:]
		}
	}
	p, err := f.describe(loc)
	if err == nil && (loc.File == "log.txt.tombstone" || loc.File == "artifact.zip.tombstone") {
		p.tombstonePlace, err = tombstoneOf(path)
	}
	p.Path, p.Line, p.Text = path, h.Line, h.Text
	return p, err
}

// rgOmission is what rg --max-columns prints in place of a line or its end.
var rgOmission = regexp.MustCompile(`^\[Omitted long (matching line|line with [0-9]+ matches)\]$| \[\.\.\. (omitted end of long line|[0-9]+ more match(es)?)\]$`)

// holding gives text, without the column rg --column printed before it, when
// line n of path holds it but for rg's omissions.
func (f placeFinder) holding(path string, n int, text string) (string, bool) {
	line, ok := f.lines.line(path, n)
	if !ok {
		return "", false
	}
	texts := []string{text}
	if column := leadingLine.FindString(text); column != "" {
		texts = append(texts, text[len(column):])
	}
	for _, t := range texts {
		if strings.Contains(line, rgOmission.ReplaceAllString(t, "")) {
			return t, true
		}
	}
	return "", false
}

// existing gives the file path names: path itself when absolute, else path
// relative to data/, the working directory or a repo dir. It checks each
// before cleaning it, so that a .. in a hit's text cannot reach a parent dir.
func (f placeFinder) existing(path string) (string, bool) {
	candidates := []string{path}
	if !filepath.IsAbs(path) {
		candidates = []string{f.data + string(filepath.Separator) + path, path}
		for _, repo := range f.repos {
			candidates = append(candidates, repo+string(filepath.Separator)+path)
		}
	}
	for _, c := range candidates {
		if _, err := os.Lstat(c); err == nil {
			abs, err := filepath.Abs(c)
			return abs, err == nil
		}
	}
	return "", false
}

func (f placeFinder) describe(loc layout.Location) (place, error) {
	runDir := filepath.Join(f.data, loc.RunDir)
	if facts, ok := f.runs[runDir]; ok {
		if p, err := f.describeWith(loc, facts); err == nil {
			return p, nil
		}
	}
	var facts runFacts
	facts.rows, facts.err = index.IndexRun(runDir)
	f.runs[runDir] = facts
	return f.describeWith(loc, facts)
}

func (f placeFinder) describeWith(loc layout.Location, facts runFacts) (place, error) {
	runDir := filepath.Join(f.data, loc.RunDir)
	rows := facts.rows
	run := rows.Run
	p := place{
		Host: loc.Host, Repo: loc.Repo, RunID: loc.RunID, Attempt: loc.Attempt,
		Workflow: run.WorkflowName, Branch: run.HeadBranch, SHA: run.HeadSHA, Event: run.Event,
		PRs: append([]int{}, run.PRNumbers...), CreatedAt: run.CreatedAt,
	}
	unread := func(what string) error {
		return errors.Join(fmt.Errorf("%s: cannot read %s", runDir, what), facts.err)
	}
	if run.RunID == 0 {
		return place{}, unread("the run")
	}
	if loc.JobDir != "" {
		i := slices.IndexFunc(rows.Jobs, func(j index.Job) bool { return j.JobID == loc.JobID && j.Attempt == loc.Attempt })
		if i < 0 {
			return place{}, unread("its job " + filepath.Base(loc.JobDir))
		}
		p.jobPlace = jobOf(runDir, rows, rows.Jobs[i])
	}
	if loc.ArtifactDir != "" {
		i := slices.IndexFunc(rows.Artifacts, func(a index.Artifact) bool { return a.ArtifactID == loc.ArtifactID })
		if i < 0 {
			return place{}, unread("its artifact " + filepath.Base(loc.ArtifactDir))
		}
		p.artifactPlace = artifactOf(rows.Artifacts[i])
	}
	var htmlFrom string
	switch {
	case loc.JobDir != "":
		htmlFrom = filepath.Join(f.data, loc.JobDir, "job.json")
	case loc.AttemptDir != "":
		htmlFrom = filepath.Join(f.data, loc.AttemptDir, "attempt.json")
	case run.LatestAttempt > 0:
		htmlFrom = filepath.Join(runDir, layout.AttemptDir("", run.LatestAttempt), "attempt.json")
	default:
		return p, nil
	}
	var err error
	p.HTMLURL, err = htmlURL(htmlFrom)
	return p, err
}

func jobOf(runDir string, rows index.Rows, job index.Job) *jobPlace {
	p := &jobPlace{
		JobID: job.JobID, Job: job.Name, JobConclusion: job.Conclusion,
		CarriedForward: job.Kind == model.CarriedForward, OriginalJobID: job.OriginalJobID,
	}
	i := slices.IndexFunc(rows.Jobs, func(j index.Job) bool { return j.JobID == job.OriginalJobID })
	if i >= 0 && rows.Jobs[i].HasLog {
		p.OriginalLog = filepath.Join(runDir, rows.Jobs[i].Path, "log.txt")
	}
	return p
}

func artifactOf(a index.Artifact) *artifactPlace {
	p := &artifactPlace{ArtifactID: a.ArtifactID, Artifact: a.Name, Attribution: a.Attribution}
	if a.AttributedAttempt > 0 {
		p.AttributedAttempt = &a.AttributedAttempt
	}
	return p
}

func htmlURL(path string) (*string, error) {
	var fields struct {
		HTMLURL *string `json:"html_url"`
	}
	return fields.HTMLURL, readJSONFile(path, &fields)
}

func tombstoneOf(path string) (*tombstonePlace, error) {
	var ts tombstone.Tombstone
	err := readJSONFile(path, &ts)
	return &tombstonePlace{Reason: ts.Reason, HTTPStatus: ts.HTTPStatus, Message: ts.Message}, err
}

func readJSONFile(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func openFile(path string) (io.ReadSeekCloser, error) {
	return os.Open(path)
}

func newLineReader(open func(path string) (io.ReadSeekCloser, error)) *lineReader {
	return &lineReader{open: open}
}

// lineReader reads one file at a time. It keeps where each line it has read
// starts, so that it reads each line about once, whatever order hits are in.
type lineReader struct {
	open func(path string) (io.ReadSeekCloser, error)
	path string
	file io.ReadSeekCloser
	r    *bufio.Reader
	// at is the offset r reads from, and starts[i] where line i+1 starts.
	at     int64
	starts []int64
}

// line gives line n of the file at path.
func (l *lineReader) line(path string, n int) (string, bool) {
	if path != l.path {
		l.close()
		file, err := l.open(path)
		if err != nil {
			return "", false
		}
		l.path, l.file, l.r, l.starts = path, file, bufio.NewReader(file), []int64{0}
	}
	i := min(n, len(l.starts)) - 1
	if l.at != l.starts[i] {
		if _, err := l.file.Seek(l.starts[i], io.SeekStart); err != nil {
			return "", false
		}
		l.r.Reset(l.file)
		l.at = l.starts[i]
	}
	for {
		line, err := l.r.ReadString('\n')
		if line == "" && err != nil {
			return "", false
		}
		l.at += int64(len(line))
		i++
		if i == len(l.starts) {
			l.starts = append(l.starts, l.at)
		}
		if i == n {
			return strings.TrimSuffix(line, "\n"), true
		}
	}
}

func (l *lineReader) close() {
	if l.file != nil {
		_ = l.file.Close()
	}
	*l = lineReader{open: l.open}
}
