package index

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/model"
)

// Flip is a rerun flip, with its run's head SHA.
type Flip struct {
	model.Flip
	HeadSHA string
}

// RerunFlips gives the rerun flips among the jobs Paths selects as UnitJob.
func (ix *Index) RerunFlips(ctx context.Context, f Filter) ([]Flip, error) {
	w := jobSource.where(f)
	query := "SELECT r.run_id, r.head_sha, x.attempt, x.job_id, x.name, x.kind, x.conclusion, x.has_log, x.path, s.name, s.conclusion FROM " +
		jobSource.from + " LEFT JOIN steps s ON s.path = x.path" + w.clause() + " ORDER BY x.path, s.number"
	jobs, restarted, err := readOrStartOver(ctx, ix, func() (attemptJobs, error) { return ix.attemptJobs(ctx, query, w.args) })
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	var flips []Flip
	for _, flip := range model.RerunFlips(jobs.jobs) {
		flips = append(flips, Flip{Flip: flip, HeadSHA: jobs.headSHAs[flip.RunID]})
	}
	return flips, restarted
}

type attemptJobs struct {
	jobs     []model.AttemptJob
	headSHAs map[int64]string
}

// attemptJobs reads the jobs, with their steps, that the query selects, one row per step.
func (ix *Index) attemptJobs(ctx context.Context, query string, args []any) (attemptJobs, error) {
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
			hasLog                   bool
			stepName, stepConclusion sql.NullString
		)
		if err := rows.Scan(&j.RunID, &sha, &j.Attempt, &j.Job.ID, &j.Job.Name, &j.Kind, &j.Job.Conclusion, &hasLog, &path, &stepName, &stepConclusion); err != nil {
			return attemptJobs{}, errors.Join(ix.dbError(err), rows.Close())
		}
		if path != lastPath {
			if hasLog {
				j.Log = filepath.Join(path, "log.txt")
			}
			read.jobs = append(read.jobs, j)
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
