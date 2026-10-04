package mirror

import "slices"

// PlanAttempts lists the attempts up to runAttempt that are not on disk, oldest first.
func PlanAttempts(runAttempt int, onDisk []int) []int {
	var missing []int
	for n := 1; n <= runAttempt; n++ {
		if !slices.Contains(onDisk, n) {
			missing = append(missing, n)
		}
	}
	return missing
}
