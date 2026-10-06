package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/model"
)

// Filter selects units. Each field left empty selects every unit; values
// within a field are alternatives.
type Filter struct {
	// Branches match only runs whose head repository is the repository.
	Branches []string
	// SHAs are prefixes of a head SHA.
	SHAs []string
	PRs  []int
	// Workflows are names. A run matches when any run of its workflow has one.
	Workflows []string
	// Jobs are globs of job names. An attempt matches when it holds a matching
	// job; a run, artifact or extracted file when its run does.
	Jobs   []string
	Events []string
	// Conclusions are of the unit's job, else attempt, else the run's latest attempt.
	Conclusions []string
	// Since and Until bound the unit's time when not zero.
	Since, Until time.Time
}

// Unit names the files Paths gives of each selected unit.
type Unit string

const (
	// UnitDefault is UnitLog and UnitExtracted together.
	UnitDefault Unit = ""
	// UnitRun is every .json file of the run outside extracted/ trees.
	UnitRun     Unit = "run"
	UnitAttempt Unit = "attempt"
	UnitJob     Unit = "job"
	UnitLog     Unit = "log"
	// UnitArtifact is artifact.zip.
	UnitArtifact Unit = "artifact"
	// UnitExtracted is every regular file of extracted/ but its .lg-extract.json.
	UnitExtracted Unit = "extracted"
)

// source is how to select the dirs of a unit: from runs r joined to the
// unit's table x, with the unit's columns. Its files give the unit's regular
// files at such a dir.
type source struct {
	from, path string
	// when is the unit's time: run created_at, attempt run_started_at, or artifact created_at.
	when, attempt, id, conclusion string
	// jobs is the dir whose jobs Filter.Jobs matches, or "" when x is the job.
	jobs  string
	files func(dir string) ([]string, error)
}

// below selects the rows whose path column child is below the dir in column parent.
func below(child, parent string) string {
	// '0' follows '/', so the range holds exactly the paths below parent.
	return fmt.Sprintf("%[1]s > %[2]s || '/' AND %[1]s < %[2]s || '0'", child, parent)
}

// within selects the rows of table x below the dir of run r.
var within = below("x.path", "r.path")

// latestConclusion is the conclusion of run r's latest attempt.
const latestConclusion = "(SELECT conclusion FROM attempts WHERE path = r.path || '/attempt-' || r.latest_attempt)"

var (
	jobSource = source{
		from: "runs r JOIN jobs x ON " + within + " JOIN attempts a ON a.path = r.path || '/attempt-' || x.attempt", path: "x.path",
		when: "a.run_started_at", attempt: "x.attempt", id: "x.job_id", conclusion: "x.conclusion",
	}
	artifactSource = source{
		from: "runs r JOIN artifacts x ON " + within, path: "x.path",
		when: "x.created_at", attempt: "NULL", id: "x.artifact_id", conclusion: latestConclusion, jobs: "r.path",
	}
	sources = map[Unit]source{
		UnitRun: {
			from: "runs r", path: "r.path",
			when: "r.created_at", attempt: "NULL", id: "NULL", conclusion: latestConclusion, jobs: "r.path",
			files: runJSON,
		},
		UnitAttempt: {
			from: "runs r JOIN attempts x ON " + within, path: "x.path",
			when: "x.run_started_at", attempt: "x.attempt", id: "NULL", conclusion: "x.conclusion", jobs: "x.path",
			files: named("attempt.json", "jobs.json", "artifacts.json", "fetch.json"),
		},
		UnitJob:       jobSource.giving(named("job.json")),
		UnitLog:       jobSource.giving(named("log.txt")),
		UnitArtifact:  artifactSource.giving(named("artifact.zip")),
		UnitExtracted: artifactSource.giving(extractedFiles),
	}
)

func (s source) giving(files func(dir string) ([]string, error)) source {
	s.files = files
	return s
}

// Paths gives the regular files of the units f selects, ordered by unit
// time, run id, attempt, and job or artifact id. With them it returns the
// error of each file or dir it could not read.
func (ix *Index) Paths(ctx context.Context, f Filter, u Unit) ([]string, error) {
	units := []Unit{u}
	if u == UnitDefault {
		units = []Unit{UnitLog, UnitExtracted}
	}
	var queries []string
	var args []any
	for _, unit := range units {
		s, ok := sources[unit]
		if !ok {
			return nil, errors.New("unknown unit " + string(unit))
		}
		query, queryArgs := s.query(unit, f)
		queries = append(queries, query)
		args = append(args, queryArgs...)
	}
	query := "SELECT unit, path FROM (" + strings.Join(queries, " UNION ALL ") + ") ORDER BY at, run, attempt, id, path"
	dirs, err := ix.unitDirs(ctx, query, args)
	var restarted error
	if unreadable(err) {
		restarted = ix.orStartOver(ctx, func() error { return err })
		dirs, err = ix.unitDirs(ctx, query, args)
	}
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	var paths []string
	unread := []error{restarted}
	for _, d := range dirs {
		files, err := sources[d.unit].files(d.dir)
		paths = append(paths, files...)
		unread = append(unread, err)
	}
	return paths, errors.Join(unread...)
}

type unitDir struct {
	unit Unit
	dir  string
}

// unitDirs gives the unit dirs the query selects.
func (ix *Index) unitDirs(ctx context.Context, query string, args []any) ([]unitDir, error) {
	rows, err := ix.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, ix.dbError(err)
	}
	var dirs []unitDir
	for rows.Next() {
		var d unitDir
		if err := rows.Scan(&d.unit, &d.dir); err != nil {
			return nil, errors.Join(ix.dbError(err), rows.Close())
		}
		dirs = append(dirs, d)
	}
	return dirs, ix.dbError(errors.Join(rows.Err(), rows.Close()))
}

// query selects the unit's dirs that f selects, with the columns Paths orders them by.
func (s source) query(unit Unit, f Filter) (string, []any) {
	var w where
	if len(f.Branches) > 0 {
		w.add("NOT r.from_fork")
		anyOf(&w, "r.head_branch IN (SELECT value FROM json_each(?))", f.Branches)
	}
	anyOf(&w, `EXISTS (SELECT 1 FROM json_each(?) WHERE r.head_sha LIKE value ESCAPE '\')`, likePrefixes(f.SHAs))
	anyOf(&w, "EXISTS (SELECT 1 FROM json_each(r.pr_numbers) p JOIN json_each(?) v ON p.value = v.value)", f.PRs)
	anyOf(&w, "r.workflow_id IN (SELECT workflow_id FROM runs WHERE workflow_name IN (SELECT value FROM json_each(?)))", f.Workflows)
	anyOf(&w, "r.event IN (SELECT value FROM json_each(?))", f.Events)
	anyOf(&w, s.conclusion+" IN (SELECT value FROM json_each(?))", f.Conclusions)
	if s.jobs == "" {
		anyOf(&w, "EXISTS (SELECT 1 FROM json_each(?) WHERE x.name GLOB value)", f.Jobs)
	} else {
		anyOf(&w, "EXISTS (SELECT 1 FROM jobs j JOIN json_each(?) g ON j.name GLOB g.value WHERE "+below("j.path", s.jobs)+")", f.Jobs)
	}
	// Unit times are whole seconds, as timeText formats the bounds.
	if !f.Since.IsZero() {
		w.add(s.when+" >= ?", timeText(roundUp(f.Since)))
	}
	if !f.Until.IsZero() {
		w.add(s.when+" <= ?", timeText(f.Until))
	}
	query := fmt.Sprintf("SELECT %s AS at, r.run_id AS run, %s AS attempt, %s AS id, '%s' AS unit, %s AS path FROM %s",
		s.when, s.attempt, s.id, unit, s.path, s.from)
	if len(w.conditions) > 0 {
		query += " WHERE " + strings.Join(w.conditions, " AND ")
	}
	return query, w.args
}

// roundUp gives t, or the next whole second after it.
func roundUp(t time.Time) time.Time {
	whole := t.Truncate(time.Second)
	if whole.Equal(t) {
		return t
	}
	return whole.Add(time.Second)
}

// where collects the conditions of a WHERE clause and their arguments.
type where struct {
	conditions []string
	args       []any
}

func (w *where) add(condition string, args ...any) {
	w.conditions = append(w.conditions, condition)
	w.args = append(w.args, args...)
}

// anyOf adds the condition, whose one ? is bound to the values as a JSON
// array, unless there are none. One parameter keeps the expression small
// however many values there are.
func anyOf[T any](w *where, condition string, values []T) {
	if len(values) == 0 {
		return
	}
	array, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	w.add(condition, string(array))
}

// likePrefixes gives LIKE patterns, escaped with \, that match strings starting with each prefix.
func likePrefixes(prefixes []string) []string {
	escape := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	patterns := make([]string, len(prefixes))
	for i, p := range prefixes {
		patterns[i] = escape.Replace(p) + "%"
	}
	return patterns
}

// runJSON gives the .json files of the run at dir, outside extracted/ trees.
func runJSON(dir string) ([]string, error) {
	return walk(dir, func(path string, d fs.DirEntry) (bool, error) {
		if d.IsDir() && d.Name() == "extracted" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "artifacts" {
			return false, filepath.SkipDir
		}
		return strings.HasSuffix(path, ".json"), nil
	})
}

// extractedFiles gives the files of the artifact's extracted/ tree but its .lg-extract.json.
func extractedFiles(dir string) ([]string, error) {
	extracted := filepath.Join(dir, "extracted")
	manifest := filepath.Join(extracted, ".lg-extract.json")
	return walk(extracted, func(path string, d fs.DirEntry) (bool, error) { return path != manifest, nil })
}

// named gives the files of a dir that are among names.
func named(names ...string) func(dir string) ([]string, error) {
	return func(dir string) ([]string, error) { return regular(dir, names...) }
}

// walk gives the regular files below root that keep accepts, skipping what is
// gone, and the error of each dir it cannot read.
func walk(root string, keep func(path string, d fs.DirEntry) (bool, error)) ([]string, error) {
	var files []string
	var unread []error
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				unread = append(unread, err)
			}
			return nil
		}
		ok, err := keep(path, d)
		if ok && d.Type().IsRegular() {
			files = append(files, path)
		}
		return err
	})
	return files, errors.Join(append(unread, err)...)
}

// regular gives those of the named files in dir that are regular files, and
// the error of each it cannot stat but for being gone.
func regular(dir string, names ...string) ([]string, error) {
	var files []string
	var unread []error
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				unread = append(unread, err)
			}
			continue
		}
		if info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files, errors.Join(unread...)
}

// Flip is a rerun flip, with its run's head SHA.
type Flip struct {
	model.Flip
	HeadSHA string
}

func (ix *Index) RerunFlips(ctx context.Context, f Filter) ([]Flip, error) { return nil, nil }
