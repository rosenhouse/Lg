package mirror

import (
	"iter"
	"slices"

	"github.com/rosenhouse/lg/internal/github"
)

// Plan yields the completed attempts of a listed run that are not on disk,
// oldest first. Only the latest attempt can be incomplete. Attempt 1 exists
// even when the listing gives no run_attempt.
func Plan(run github.Run, onDisk []int) iter.Seq[int] {
	latest := max(run.RunAttempt, 1)
	if run.Status != "completed" {
		latest--
	}
	return func(yield func(int) bool) {
		for n := 1; n <= latest; n++ {
			if !slices.Contains(onDisk, n) && !yield(n) {
				return
			}
		}
	}
}
