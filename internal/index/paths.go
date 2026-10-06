package index

import (
	"context"
	"time"
)

// Filter selects units. Each field left empty selects every unit; values
// within a field are alternatives.
type Filter struct {
	Branches, SHAs, Workflows, Jobs, Events, Conclusions []string
	PRs                                                  []int
	Since, Until                                         time.Time
}

// Unit names the files Paths gives of each selected unit.
type Unit string

const (
	// UnitDefault is UnitLog and UnitExtracted together.
	UnitDefault   Unit = ""
	UnitRun       Unit = "run"
	UnitAttempt   Unit = "attempt"
	UnitJob       Unit = "job"
	UnitLog       Unit = "log"
	UnitArtifact  Unit = "artifact"
	UnitExtracted Unit = "extracted"
)

// Paths gives the regular files of the units f selects.
func (ix *Index) Paths(ctx context.Context, f Filter, u Unit) ([]string, error) {
	return nil, nil
}
