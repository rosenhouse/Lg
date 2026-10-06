package cli

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	Hits []string `arg:"" optional:"" name:"path|hit" help:"A path, or a line rg or grep printed. A relative path is resolved against the working directory, data/, or a repo dir. Without any, lg reads lines from stdin."`
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
	defer finder.close()
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
	// Conclusion is the hit's attempt's, or that of the attempt its artifact
	// is attributed to.
	Conclusion *string `json:"conclusion"`
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
	dir  string
	rows index.Rows
	err  error
}

// cachedRuns is how many runs a placeFinder keeps the facts of.
const cachedRuns = 16

// placeFinder decodes hits in the store at data. It reads a run's files once
// while the run is among the last cachedRuns that hits were in, and again
// when a hit names a unit that read lacked.
type placeFinder struct {
	data string
	// real is data with symlinks resolved.
	real  string
	repos []string
	dirs  *dirCache
	// runs are the runs hits were last in, the latest last.
	runs  []runFacts
	lines *lineReader
}

func newPlaceFinder(data string) *placeFinder {
	// A missing data/ holds no path, so its real path need not be known.
	real, _ := filepath.EvalSymlinks(data)
	return &placeFinder{data: data, real: real, repos: repoDirs(data), dirs: newDirCache(os.ReadDir), lines: newLineReader(openFile)}
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

func (f *placeFinder) find(hit string) (place, error) {
	var path string
	parse := func(stat func(string) (isDir, exists bool)) (layout.Hit, bool) {
		return layout.ParseHit(hit, func(p string) (isDir, found bool) {
			path, isDir, found = f.existing(p, stat)
			return isDir, found
		})
	}
	h, ok := parse(f.dirs.stat)
	if !ok {
		// f.dirs lacks files created since it listed their dirs.
		h, ok = parse(stat)
	}
	if !ok {
		if leadingLine.MatchString(hit) {
			return place{}, fmt.Errorf("%q names no file; run rg with -H --no-heading to print file names", hit)
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
		text, held := f.holding(path, h.Line, h.Text)
		if !held {
			text = hit[len(h.Path)+1:]
			if !f.anyHolding(path, text) {
				if h.Text == "" {
					return place{}, fmt.Errorf("%s has no line %d", path, h.Line)
				}
				return place{}, fmt.Errorf("%q: no line of %s holds its text", hit, path)
			}
			h.Line = 0
		}
		h.Text = text
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
// line n of path holds it.
func (f *placeFinder) holding(path string, n int, text string) (string, bool) {
	line, ok := f.lines.line(path, n)
	if !ok {
		return "", false
	}
	texts := []string{text}
	if column := leadingLine.FindString(text); column != "" {
		texts = append(texts, text[len(column):])
	}
	for _, t := range texts {
		if holds(line, t) {
			return t, true
		}
	}
	return "", false
}

// anyHolding tells whether any line of path holds text.
func (f *placeFinder) anyHolding(path, text string) bool {
	for n := 1; ; n++ {
		line, ok := f.lines.line(path, n)
		if !ok {
			return false
		}
		if holds(line, text) {
			return true
		}
	}
}

// holds tells whether line holds text but for rg's omissions.
func holds(line, text string) bool {
	return strings.Contains(line, rgOmission.ReplaceAllString(text, ""))
}

// existing gives the file path names: path itself when absolute, else path
// relative to the working directory, data/ or a repo dir. It checks each
// before cleaning it, so that a .. in a hit's text cannot reach a parent dir.
func (f *placeFinder) existing(path string, stat func(string) (isDir, exists bool)) (abs string, isDir, found bool) {
	candidates := []string{path}
	if !filepath.IsAbs(path) {
		candidates = []string{path, f.data + string(filepath.Separator) + path}
		for _, repo := range f.repos {
			candidates = append(candidates, repo+string(filepath.Separator)+path)
		}
	}
	for _, c := range candidates {
		if isDir, exists := stat(c); exists {
			abs, err := filepath.Abs(c)
			return abs, isDir, err == nil
		}
	}
	return "", false, false
}

// dirCache tells whether paths exist from one listing of each one's dir.
type dirCache struct {
	readDir func(dir string) ([]fs.DirEntry, error)
	// types[dir][name] is the type of the file dir+name.
	types map[string]map[string]fs.FileMode
}

func newDirCache(readDir func(dir string) ([]fs.DirEntry, error)) *dirCache {
	return &dirCache{readDir: readDir, types: map[string]map[string]fs.FileMode{}}
}

// stat is like the stat func, but misses files created since it listed their dir.
func (c *dirCache) stat(path string) (isDir, exists bool) {
	dir, name := filepath.Split(path)
	if name == "." || name == ".." {
		return stat(path)
	}
	types, ok := c.types[dir]
	if !ok {
		entries, err := c.readDir(cmp.Or(dir, "."))
		if err != nil {
			return false, false
		}
		types = map[string]fs.FileMode{}
		for _, e := range entries {
			types[e.Name()] = e.Type()
		}
		c.types[dir] = types
	}
	mode, exists := types[name]
	if mode&fs.ModeSymlink != 0 {
		return stat(path)
	}
	return mode.IsDir(), exists
}

// stat tells whether path exists and is a dir, following symlinks.
func stat(path string) (isDir, exists bool) {
	info, err := os.Stat(path)
	return err == nil && info.IsDir(), err == nil
}

func (f *placeFinder) describe(loc layout.Location) (place, error) {
	runDir := filepath.Join(f.data, loc.RunDir)
	if i := slices.IndexFunc(f.runs, func(r runFacts) bool { return r.dir == runDir }); i >= 0 {
		facts := f.runs[i]
		f.runs = slices.Delete(f.runs, i, i+1)
		if p, err := describeRun(f.data, loc, facts); err == nil {
			f.runs = append(f.runs, facts)
			return p, nil
		}
	}
	facts := runFacts{dir: runDir}
	facts.rows, facts.err = index.IndexRun(runDir)
	if len(f.runs) == cachedRuns {
		f.runs = slices.Delete(f.runs, 0, 1)
	}
	f.runs = append(f.runs, facts)
	return describeRun(f.data, loc, facts)
}

func (f *placeFinder) close() {
	f.lines.close()
}

// describeRun describes loc, in the run at facts.dir, from facts.
func describeRun(data string, loc layout.Location, facts runFacts) (place, error) {
	runDir := facts.dir
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
		i := slices.IndexFunc(rows.Jobs, func(j index.Job) bool { return j.JobID == loc.JobID })
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
	p.Conclusion = conclusionOf(rows, p)
	if facts.err != nil {
		return place{}, unread("the run")
	}
	var htmlFrom string
	switch {
	case loc.JobDir != "":
		htmlFrom = filepath.Join(data, loc.JobDir, "job.json")
	case loc.AttemptDir != "":
		htmlFrom = filepath.Join(data, loc.AttemptDir, "attempt.json")
	case run.LatestAttempt > 0:
		htmlFrom = filepath.Join(runDir, layout.AttemptDir("", run.LatestAttempt), "attempt.json")
	default:
		return p, nil
	}
	var err error
	p.HTMLURL, err = htmlURL(htmlFrom)
	return p, err
}

func conclusionOf(rows index.Rows, p place) *string {
	attempt := p.Attempt
	if p.artifactPlace != nil && p.AttributedAttempt != nil {
		attempt = *p.AttributedAttempt
	}
	i := slices.IndexFunc(rows.Attempts, func(a index.Attempt) bool { return a.Attempt == attempt })
	if i < 0 {
		return nil
	}
	return &rows.Attempts[i].Conclusion
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

// cachedFiles is how many files a lineReader keeps line starts of.
const cachedFiles = 16

// lineReader reads one file at a time. It keeps where each line it has read
// starts in the last cachedFiles files, so that it reads each line about
// once, whatever order hits are in.
type lineReader struct {
	open func(path string) (io.ReadSeekCloser, error)
	file io.ReadSeekCloser
	r    *bufio.Reader
	// at is the offset r reads from.
	at int64
	// files are the files last read, the open one last.
	files []lineStarts
}

// lineStarts holds where lines of the file at path start: starts[i] is
// where line i+1 starts.
type lineStarts struct {
	path   string
	starts []int64
}

// line gives line n of the file at path.
func (l *lineReader) line(path string, n int) (string, bool) {
	if l.file == nil || l.files[len(l.files)-1].path != path {
		if !l.switchTo(path) {
			return "", false
		}
	}
	f := &l.files[len(l.files)-1]
	i := min(n, len(f.starts)) - 1
	if l.at != f.starts[i] {
		if _, err := l.file.Seek(f.starts[i], io.SeekStart); err != nil {
			return "", false
		}
		l.r.Reset(l.file)
		l.at = f.starts[i]
	}
	for {
		line, err := l.r.ReadString('\n')
		if line == "" && err != nil {
			return "", false
		}
		l.at += int64(len(line))
		i++
		if i == len(f.starts) {
			f.starts = append(f.starts, l.at)
		}
		if i == n {
			return strings.TrimSuffix(line, "\n"), true
		}
	}
}

// switchTo opens path, with the line starts known of it, in place of the open file.
func (l *lineReader) switchTo(path string) bool {
	l.close()
	file, err := l.open(path)
	if err != nil {
		return false
	}
	l.file, l.r, l.at = file, bufio.NewReader(file), 0
	known := lineStarts{path: path, starts: []int64{0}}
	if i := slices.IndexFunc(l.files, func(f lineStarts) bool { return f.path == path }); i >= 0 {
		known = l.files[i]
		l.files = slices.Delete(l.files, i, i+1)
	}
	if len(l.files) == cachedFiles {
		l.files = slices.Delete(l.files, 0, 1)
	}
	l.files = append(l.files, known)
	return true
}

func (l *lineReader) close() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
