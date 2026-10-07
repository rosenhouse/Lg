package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

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
// attempt was cancelled. A run whose first attempt is not on disk is a gap in
// each series of its workflow and branch. With the series it returns the
// error of each log of their failures it could not read.
func (ix *Index) IntermittentFailures(ctx context.Context, f Filter) ([]Intermittent, error) {
	seriesRuns := Filter{Branches: f.Branches, Workflows: f.Workflows, Events: f.Events, Jobs: f.Jobs}
	jobs, restarted, err := ix.flakeJobs(ctx, seriesRuns,
		append([]string{below("x.path", "r.path || '/attempt-1'"), "a.conclusion IS NOT 'cancelled'"}, intermittentRuns...)...)
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	gaps, err := ix.gaps(ctx, seriesRuns)
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
	for _, s := range model.FirstAttemptSeries(jobs, gaps) {
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

// intermittentRuns are the conditions on run r for intermittent detection to count it.
var intermittentRuns = []string{"r.event NOT IN ('pull_request', 'pull_request_target')", latestConclusion + " IS NOT 'cancelled'"}

// gaps gives the runs of f's branches, workflows and events that
// intermittent detection counts and whose first attempt is not on disk,
// each at its created_at.
func (ix *Index) gaps(ctx context.Context, f Filter) ([]model.Gap, error) {
	w := sources[UnitRun].where(Filter{Branches: f.Branches, Workflows: f.Workflows, Events: f.Events})
	for _, c := range intermittentRuns {
		w.add(c)
	}
	w.add("NOT EXISTS (SELECT 1 FROM attempts WHERE path = r.path || '/attempt-1')")
	rows, err := ix.db.QueryContext(ctx, "SELECT r.run_id, r.workflow_id, r.head_sha, r.head_branch, r.created_at, r.path FROM runs r"+w.clause(), w.args...)
	if err != nil {
		return nil, ix.dbError(err)
	}
	var gaps []model.Gap
	for rows.Next() {
		var (
			g                               model.Gap
			workflowID                      sql.NullInt64
			sha, branch, createdAt, runPath sql.NullString
		)
		if err := rows.Scan(&g.RunID, &workflowID, &sha, &branch, &createdAt, &runPath); err != nil {
			return nil, errors.Join(ix.dbError(err), rows.Close())
		}
		g.WorkflowID, g.HeadSHA, g.Branch = workflowID.Int64, sha.String, branch.String
		if createdAt.Valid {
			if g.At, err = time.Parse(time.RFC3339, createdAt.String); err != nil {
				return nil, errors.Join(fmt.Errorf("%s: created_at: %w", runPath.String, err), rows.Close())
			}
		}
		gaps = append(gaps, g)
	}
	return gaps, ix.dbError(errors.Join(rows.Err(), rows.Close()))
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
