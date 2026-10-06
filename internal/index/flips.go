package index

import (
	"context"
	"database/sql"
	"errors"

	"github.com/rosenhouse/lg/internal/model"
)

// Flip is a rerun flip, with its run's head SHA.
type Flip struct {
	model.Flip
	HeadSHA string
}

// flipRuns selects runs by the start of any of their attempts and by the
// conclusion of the latest.
var flipRuns = source{
	from: "runs r JOIN attempts x ON " + within,
	when: "x.run_started_at", conclusion: latestConclusion, jobs: "r.path",
}

// RerunFlips gives the rerun flips of the runs f selects, among the jobs
// whose names match f.Jobs, comparing every attempt of each run.
func (ix *Index) RerunFlips(ctx context.Context, f Filter) ([]Flip, error) {
	w := jobSource.where(Filter{Jobs: f.Jobs})
	runs := flipRuns.where(f)
	w.add("r.run_id IN (SELECT r.run_id FROM "+flipRuns.from+runs.clause()+")", runs.args...)
	// Ordering by run first lets SQLite find each run's jobs by path range.
	query := "SELECT r.run_id, r.head_sha, x.attempt, x.job_id, x.name, x.kind, x.conclusion, x.path, s.name, s.conclusion FROM " +
		jobSource.from + " LEFT JOIN steps s ON s.path = x.path" + w.clause() + " ORDER BY r.path, x.path, s.number"
	jobs, restarted, err := readOrStartOver(ctx, ix, func() (attemptJobs, error) { return ix.readAttemptJobs(ctx, query, w.args) })
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	unread := []error{restarted}
	for i, dir := range jobs.dirs {
		logs, err := regular(dir, "log.txt")
		if len(logs) > 0 {
			jobs.jobs[i].Log = logs[0]
		}
		unread = append(unread, err)
	}
	var flips []Flip
	for _, flip := range model.RerunFlips(jobs.jobs) {
		flips = append(flips, Flip{Flip: flip, HeadSHA: jobs.headSHAs[flip.RunID]})
	}
	return flips, errors.Join(unread...)
}

// attemptJobs holds jobs read from the index, without their logs, and the dir of each.
type attemptJobs struct {
	jobs     []model.AttemptJob
	dirs     []string
	headSHAs map[int64]string
}

// readAttemptJobs reads the jobs, with their steps, that the query selects, one row per step.
func (ix *Index) readAttemptJobs(ctx context.Context, query string, args []any) (attemptJobs, error) {
	rows, err := ix.db.QueryContext(ctx, query, args...)
	if err != nil {
		return attemptJobs{}, ix.dbError(err)
	}
	read := attemptJobs{headSHAs: map[int64]string{}}
	var lastPath string
	for rows.Next() {
		var (
			j                        model.AttemptJob
			sha, path                string
			stepName, stepConclusion sql.NullString
		)
		if err := rows.Scan(&j.RunID, &sha, &j.Attempt, &j.Job.ID, &j.Job.Name, &j.Kind, &j.Job.Conclusion, &path, &stepName, &stepConclusion); err != nil {
			return attemptJobs{}, errors.Join(ix.dbError(err), rows.Close())
		}
		if path != lastPath {
			read.jobs = append(read.jobs, j)
			read.dirs = append(read.dirs, path)
			read.headSHAs[j.RunID] = sha
			lastPath = path
		}
		if stepName.Valid {
			last := &read.jobs[len(read.jobs)-1].Job
			last.Steps = append(last.Steps, model.Step{Name: stepName.String, Conclusion: stepConclusion.String})
		}
	}
	return read, ix.dbError(errors.Join(rows.Err(), rows.Close()))
}
