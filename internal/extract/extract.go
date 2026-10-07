// Package extract expands an artifact's zip, and the archives nested in it,
// into the artifact's extracted/ dir.
package extract

import (
	"time"

	"github.com/rosenhouse/lg/internal/store"
)

// Limits bound what Extract writes for one artifact.
type Limits struct {
	// MaxBytes bounds the bytes of every file written.
	MaxBytes int64
	// MaxNesting is how many levels of archives within artifact.zip are expanded.
	MaxNesting int
}

func Defaults() Limits { return Limits{} }

// Extract expands artifactDir/artifact.zip into artifactDir/extracted.
func Extract(s *store.Store, artifactDir string, limits Limits, now time.Time) error {
	return nil
}
