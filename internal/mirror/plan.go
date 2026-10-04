package mirror

import (
	"slices"

	"github.com/rosenhouse/lg/internal/github"
)

// Plan lists the completed attempts of a listed run that are not on disk,
// oldest first. Only the latest attempt can be incomplete. Attempt 1 exists
// even when the listing gives no run_attempt.
func Plan(run github.Run, onDisk []int) []int {
	latest := max(run.RunAttempt, 1)
	if run.Status != "completed" {
		latest--
	}
	var planned []int
	for n := 1; n <= latest; n++ {
		if !slices.Contains(onDisk, n) {
			planned = append(planned, n)
		}
	}
	return planned
}
