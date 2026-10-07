package index

import (
	"context"
	"errors"
	"slices"

	"github.com/rosenhouse/lg/internal/model"
)

// Intermittent is a series with the runs that failed alone in it.
type Intermittent struct {
	model.Series
	Failures []model.RunOutcome
}

// IntermittentFailures gives each series, of a job name or a step name of it,
// that failed alone in the first attempt of a run f selects. A series holds
// every run of f's branches, workflows and events, among the jobs whose names
// match f.Jobs; the rest of f selects only the failures. It leaves out
// pull_request and pull_request_target runs, and runs whose first or latest
// attempt was cancelled. With the series it returns the error of each log of their
// failures it could not read.
func (ix *Index) IntermittentFailures(ctx context.Context, f Filter) ([]Intermittent, error) {
	seriesRuns := Filter{Branches: f.Branches, Workflows: f.Workflows, Events: f.Events, Jobs: f.Jobs}
	jobs, restarted, err := ix.flakeJobs(ctx, seriesRuns, below("x.path", "r.path || '/attempt-1'"),
		"r.event NOT IN ('pull_request', 'pull_request_target')", "a.conclusion IS NOT 'cancelled'", latestConclusion+" IS NOT 'cancelled'")
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	failureRuns := Filter{SHAs: f.SHAs, PRs: f.PRs, Conclusions: f.Conclusions, Since: f.Since, Until: f.Until}
	selected, err := ix.runIDs(ctx, flakeRuns, failureRuns)
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	var found []Intermittent
	logs := newLogsOnDisk()
	for _, s := range model.FirstAttemptSeries(jobs) {
		failures := slices.DeleteFunc(s.IsolatedFailures(), func(r model.RunOutcome) bool { return !selected[r.RunID] })
		for i := range failures {
			failures[i].Logs = logs.keep(failures[i].Logs)
		}
		if len(failures) > 0 {
			found = append(found, Intermittent{Series: s, Failures: failures})
		}
	}
	return found, errors.Join(restarted, logs.err())
}

// runIDs gives the ids of the runs s selects with f.
func (ix *Index) runIDs(ctx context.Context, s source, f Filter) (map[int64]bool, error) {
	w := s.where(f)
	rows, err := ix.db.QueryContext(ctx, "SELECT r.run_id FROM "+s.from+w.clause(), w.args...)
	if err != nil {
		return nil, ix.dbError(err)
	}
	ids := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, errors.Join(ix.dbError(err), rows.Close())
		}
		ids[id] = true
	}
	return ids, ix.dbError(errors.Join(rows.Err(), rows.Close()))
}
