package index

import (
	"context"
	"errors"

	"github.com/rosenhouse/lg/internal/model"
)

// FirstAttemptOutcomes gives the series of each job name, and each step name
// of it, over the first attempts of the runs f selects, among the jobs whose
// names match f.Jobs. It leaves out pull_request and pull_request_target
// runs, and runs whose first attempt was cancelled. With the series it
// returns the error of each of their logs it could not read.
func (ix *Index) FirstAttemptOutcomes(ctx context.Context, f Filter) ([]model.Series, error) {
	jobs, restarted, err := ix.flakeJobs(ctx, f, "x.attempt = 1",
		"r.event NOT IN ('pull_request', 'pull_request_target')", "a.conclusion IS NOT 'cancelled'")
	if err != nil {
		return nil, errors.Join(restarted, err)
	}
	series := model.FirstAttemptSeries(jobs)
	logs := logsOnDisk{unread: []error{restarted}}
	for _, s := range series {
		for i := range s.Runs {
			s.Runs[i].Logs = logs.keep(s.Runs[i].Logs)
		}
	}
	return series, errors.Join(logs.unread...)
}

// Intermittent is a series with the runs that failed alone in it.
type Intermittent struct {
	model.Series
	Failures []model.RunOutcome
}

func (ix *Index) IntermittentFailures(ctx context.Context, f Filter) ([]Intermittent, error) {
	return nil, nil
}
