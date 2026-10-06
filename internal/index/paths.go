package index

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Filter selects units. Each field left empty selects every unit; values
// within a field are alternatives.
type Filter struct {
	// Branches match only runs whose head repository is the repository.
	Branches []string
	// SHAs are prefixes of a head SHA.
	SHAs []string
	PRs  []int
	// Workflows are workflow names.
	Workflows []string
	// Jobs are globs of job names.
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

// source is how to select a unit: its tables, bound as r for runs and x for
// the unit's table, and its columns.
type source struct {
	unit       Unit
	from, path string
	// when is the unit's time: run created_at, attempt run_started_at, or artifact created_at.
	when, attempt, id, conclusion string
	// jobs is the dir whose jobs --job matches, or "" when x is the job.
	jobs string
}

// within selects the rows of table x below the dir of run r.
const within = "x.path > r.path || '/' AND x.path < r.path || '0'"

// latestConclusion is the conclusion of run r's latest attempt.
const latestConclusion = "(SELECT conclusion FROM attempts WHERE path = r.path || '/attempt-' || r.latest_attempt)"

var sources = map[Unit]source{
	UnitRun: {
		from: "runs r", path: "r.path",
		when: "r.created_at", attempt: "NULL", id: "NULL", conclusion: latestConclusion, jobs: "r.path",
	},
	UnitAttempt: {
		from: "runs r JOIN attempts x ON " + within, path: "x.path",
		when: "x.run_started_at", attempt: "x.attempt", id: "NULL", conclusion: "x.conclusion", jobs: "x.path",
	},
	UnitJob: {
		from: "runs r JOIN jobs x ON " + within + " JOIN attempts a ON a.path = r.path || '/attempt-' || x.attempt", path: "x.path",
		when: "a.run_started_at", attempt: "x.attempt", id: "x.job_id", conclusion: "x.conclusion",
	},
	UnitArtifact: {
		from: "runs r JOIN artifacts x ON " + within, path: "x.path",
		when: "x.created_at", attempt: "NULL", id: "x.artifact_id", conclusion: latestConclusion, jobs: "r.path",
	},
}

func init() {
	for unit, s := range map[Unit]Unit{UnitLog: UnitJob, UnitExtracted: UnitArtifact} {
		sources[unit] = sources[s]
	}
	for unit, s := range sources {
		s.unit = unit
		sources[unit] = s
	}
}

// selects adds what a unit needs beyond its row.
var selects = map[Unit]string{UnitLog: "x.has_log", UnitArtifact: "x.has_zip", UnitExtracted: "x.extracted"}

// Paths gives the regular files of the units f selects, ordered by unit
// time, run id, attempt, and job or artifact id.
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
		query, queryArgs := s.query(f)
		queries = append(queries, query)
		args = append(args, queryArgs...)
	}
	rows, err := ix.db.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY 1, 2, 3, 4, 6", args...)
	if err != nil {
		return nil, ix.dbError(err)
	}
	type unitDir struct {
		unit Unit
		dir  string
	}
	var dirs []unitDir
	for rows.Next() {
		var d unitDir
		var when, run, attempt, id any
		if err := rows.Scan(&when, &run, &attempt, &id, &d.unit, &d.dir); err != nil {
			return nil, errors.Join(ix.dbError(err), rows.Close())
		}
		dirs = append(dirs, d)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, ix.dbError(err)
	}
	var paths []string
	for _, d := range dirs {
		files, err := unitFiles(d.unit, d.dir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, files...)
	}
	return paths, nil
}

func (s source) query(f Filter) (string, []any) {
	var w where
	if len(f.Branches) > 0 {
		w.add("NOT r.from_fork")
		in(&w, "r.head_branch", f.Branches)
	}
	w.anyOf("r.head_sha LIKE ? ESCAPE '\\'", likePrefixes(f.SHAs))
	if len(f.PRs) > 0 {
		var prs where
		in(&prs, "value", f.PRs)
		w.add("EXISTS (SELECT 1 FROM json_each(r.pr_numbers) WHERE "+prs.conditions[0]+")", prs.args...)
	}
	in(&w, "r.workflow_name", f.Workflows)
	in(&w, "r.event", f.Events)
	in(&w, s.conclusion, f.Conclusions)
	if s.jobs == "" {
		w.anyOf("x.name GLOB ?", f.Jobs)
	} else if len(f.Jobs) > 0 {
		var jobs where
		jobs.anyOf("j.name GLOB ?", f.Jobs)
		w.add("EXISTS (SELECT 1 FROM jobs j WHERE j.path > "+s.jobs+" || '/' AND j.path < "+s.jobs+" || '0' AND "+jobs.conditions[0]+")", jobs.args...)
	}
	if !f.Since.IsZero() {
		w.add(s.when+" >= ?", timeText(f.Since))
	}
	if !f.Until.IsZero() {
		w.add(s.when+" <= ?", timeText(f.Until))
	}
	if sel, ok := selects[s.unit]; ok {
		w.add(sel)
	}
	query := "SELECT " + strings.Join([]string{s.when, "r.run_id", s.attempt, s.id, "'" + string(s.unit) + "'", s.path}, ", ") +
		" FROM " + s.from
	if len(w.conditions) > 0 {
		query += " WHERE " + strings.Join(w.conditions, " AND ")
	}
	return query, w.args
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

// in adds column IN values, unless there are none.
func in[T any](w *where, column string, values []T) {
	if len(values) == 0 {
		return
	}
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	w.add(column+" IN (?"+strings.Repeat(", ?", len(values)-1)+")", args...)
}

// anyOf adds the condition, holding one ?, for any of the values, unless there are none.
func (w *where) anyOf(condition string, values []string) {
	if len(values) == 0 {
		return
	}
	var alternatives []string
	for _, v := range values {
		alternatives = append(alternatives, condition)
		w.args = append(w.args, v)
	}
	w.conditions = append(w.conditions, "("+strings.Join(alternatives, " OR ")+")")
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

// unitFiles gives the regular files of the unit at dir.
func unitFiles(u Unit, dir string) ([]string, error) {
	switch u {
	case UnitRun:
		return walk(dir, func(path string, d fs.DirEntry) (bool, error) {
			if d.IsDir() && d.Name() == "extracted" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "artifacts" {
				return false, filepath.SkipDir
			}
			return strings.HasSuffix(path, ".json"), nil
		})
	case UnitExtracted:
		extracted := filepath.Join(dir, "extracted")
		manifest := filepath.Join(extracted, ".lg-extract.json")
		return walk(extracted, func(path string, d fs.DirEntry) (bool, error) { return path != manifest, nil })
	case UnitAttempt:
		return regular(dir, "attempt.json", "jobs.json", "artifacts.json", "fetch.json")
	case UnitJob:
		return regular(dir, "job.json")
	case UnitLog:
		return regular(dir, "log.txt")
	}
	return regular(dir, "artifact.zip")
}

// walk gives the regular files below root that keep accepts, or none when root is gone.
func walk(root string, keep func(path string, d fs.DirEntry) (bool, error)) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ok, err := keep(path, d)
		if ok && d.Type().IsRegular() {
			files = append(files, path)
		}
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return files, err
}

// regular gives those of the named files in dir that are regular files.
func regular(dir string, names ...string) ([]string, error) {
	var files []string
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files, nil
}
