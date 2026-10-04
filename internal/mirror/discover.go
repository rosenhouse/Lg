package mirror

import (
	"context"
	"time"

	"github.com/rosenhouse/lg/internal/github"
)

// Discover lists the runs created in [from, to].
func Discover(ctx context.Context, gh github.Client, from, to time.Time) ([]github.Run, error) {
	runs, _, err := gh.ListRuns(ctx, github.RunQuery{From: from, To: to, PerPage: 100})
	return runs, err
}
