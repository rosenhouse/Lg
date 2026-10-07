package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/rosenhouse/lg/internal/model"
)

// Flip is a rerun flip, with its run's head SHA.
type Flip struct {
	model.Flip
	HeadSHA string
}

// flakeRuns selects runs by the start of any of their attempts and by the
// conclusion of the latest. It takes no Filter.Jobs, which selects jobs, not runs.
var flakeRuns = source{
	from: "runs r JOIN attempts x ON " + within,
	when: "x.run_started_at", conclusion: latestConclusion,
}

// RerunFlips gives the rerun flips of the runs f selects, among the jobs
// whose names match f.Jobs, comparing every attempt of each run. With them it
// returns the error of each of their logs it could not read.
func (ix *Index) RerunFlips(ctx context.Context, f Filter) ([]Flip, error) {
	// A run with one attempt on disk cannot flip.
	jobs, restarted, err := ix.flakeJobs(ctx, f, "r.latest_attempt > 1")
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	headSHAs := map[int64]string{}
	attemptJobs := make([]model.AttemptJob, len(jobs))
	for i, j := range jobs {
		attemptJobs[i] = j.AttemptJob
		headSHAs[j.RunID] = j.HeadSHA
	}
	var flips []Flip
	logs := newLogsOnDisk()
	for _, flip := range model.RerunFlips(attemptJobs) {
		flip.Logs = logs.keep(flip.Logs)
		flips = append(flips, Flip{Flip: flip, HeadSHA: headSHAs[flip.RunID]})
	}
	return flips, errors.Join(restarted, logs.err())
}

// logsOnDisk keeps the logs that are regular files, and the error of each it
// could not stat. It looks at each log once.
type logsOnDisk struct {
	regular map[string]bool
	unread  []error
}

func newLogsOnDisk() *logsOnDisk { return &logsOnDisk{regular: map[string]bool{}} }

func (d *logsOnDisk) keep(logs []string) []string {
	return slices.DeleteFunc(logs, func(log string) bool {
		if _, seen := d.regular[log]; !seen {
			files, err := regular(filepath.Dir(log), filepath.Base(log))
			d.regular[log] = len(files) > 0
			d.unread = append(d.unread, err)
		}
		return !d.regular[log]
	})
}

func (d *logsOnDisk) err() error { return errors.Join(d.unread...) }

// flakeJobs reads, with their steps, the jobs of the runs f selects whose
// names match f.Jobs and that meet the conditions. If SQLite cannot read
// lg.db, it starts over and gives that error as restarted.
func (ix *Index) flakeJobs(ctx context.Context, f Filter, conditions ...string) (jobs []model.RunJob, restarted, err error) {
	w := jobSource.where(Filter{Jobs: f.Jobs})
	runFilter := f
	runFilter.Jobs = nil
	runs := flakeRuns.where(runFilter)
	w.add("r.run_id IN (SELECT r.run_id FROM "+flakeRuns.from+runs.clause()+")", runs.args...)
	for _, c := range conditions {
		w.add(c)
	}
	// Ordering by run first lets SQLite find each run's jobs by path range.
	query := "SELECT r.run_id, r.head_sha, r.workflow_id, r.workflow_name, r.head_branch, a.run_started_at, " +
		"x.attempt, x.job_id, x.name, x.kind, x.conclusion, x.path, s.name, s.conclusion FROM " +
		jobSource.from + " LEFT JOIN steps s ON s.path = x.path" + w.clause() + " ORDER BY r.path, x.path, s.number"
	return readOrStartOver(ctx, ix, func() ([]model.RunJob, error) { return ix.readJobs(ctx, query, w.args) })
}

// readJobs reads the jobs, with their steps, that the query selects, one row per step.
func (ix *Index) readJobs(ctx context.Context, query string, args []any) ([]model.RunJob, error) {
	rows, err := ix.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, ix.dbError(err)
	}
	var jobs []model.RunJob
	var lastPath string
	for rows.Next() {
		var (
			j                         model.RunJob
			workflowID                sql.NullInt64
			workflow, branch, started sql.NullString
			path                      string
			stepName, stepConclusion  sql.NullString
		)
		if err := rows.Scan(&j.RunID, &j.HeadSHA, &workflowID, &workflow, &branch, &started,
			&j.Attempt, &j.Job.ID, &j.Job.Name, &j.Kind, &j.Job.Conclusion, &path, &stepName, &stepConclusion); err != nil {
			return nil, errors.Join(ix.dbError(err), rows.Close())
		}
		if path != lastPath {
			j.WorkflowID, j.Workflow, j.Branch = workflowID.Int64, workflow.String, branch.String
			// An attempt with no run_started_at starts at the zero time.
			if started.Valid {
				if j.StartedAt, err = time.Parse(time.RFC3339, started.String); err != nil {
					return nil, errors.Join(fmt.Errorf("%s: run_started_at: %w", path, err), rows.Close())
				}
			}
			j.Log = filepath.Join(path, "log.txt")
			jobs = append(jobs, j)
			lastPath = path
		}
		if stepName.Valid {
			last := &jobs[len(jobs)-1].Job
			last.Steps = append(last.Steps, model.Step{Name: stepName.String, Conclusion: stepConclusion.String})
		}
	}
	return jobs, ix.dbError(errors.Join(rows.Err(), rows.Close()))
}
