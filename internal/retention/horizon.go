package retention

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/store"
)

// Horizon is state/horizon.json: the newest created_at of a run that
// disk_cap evicted. Losing it costs one re-download of the evicted runs.
type Horizon struct {
	At time.Time `json:"horizon"`
}

func horizonFile(s *store.Store) string { return filepath.Join(s.State(), "horizon.json") }

// ReadHorizon gives the zero Horizon, which skips nothing, when the file is missing.
func ReadHorizon(s *store.Store) (Horizon, error) {
	var h Horizon
	raw, err := os.ReadFile(horizonFile(s))
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, fmt.Errorf("%s: %w", horizonFile(s), err)
	}
	return h, nil
}

func (h Horizon) Write(s *store.Store) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return s.ReplaceFile(horizonFile(s), append(raw, '\n'))
}

// Skips reports whether a run created at createdAt is at or before the horizon.
func (h Horizon) Skips(createdAt time.Time) bool { return !createdAt.After(h.At) }
