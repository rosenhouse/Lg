package retention

import (
	"encoding/json"
	"errors"
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

const horizonFile = "horizon.json"

// ReadHorizon gives the zero Horizon, which skips nothing, when the file is
// missing. One that does not parse is moved aside, and ReadHorizon gives the
// zero Horizon and the parse error as discarded.
func ReadHorizon(s *store.Store) (h Horizon, discarded, err error) {
	discarded, err = s.ReadState(horizonFile, func(raw []byte) error { return json.Unmarshal(raw, &h) })
	if discarded != nil || err != nil {
		return Horizon{}, discarded, err
	}
	return h, nil, nil
}

// PeekHorizon reads the horizon in state without the write lock. It leaves
// a corrupt file for ReadHorizon to move aside, and skips nothing for it, as
// ReadHorizon does.
func PeekHorizon(state string) (Horizon, error) {
	raw, err := os.ReadFile(filepath.Join(state, horizonFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Horizon{}, nil
	}
	if err != nil {
		return Horizon{}, err
	}
	var h Horizon
	if json.Unmarshal(raw, &h) != nil {
		return Horizon{}, nil
	}
	return h, nil
}

func (h Horizon) Write(s *store.Store) error { return s.WriteState(horizonFile, h) }

// Skips reports whether a run created at createdAt is at or before the horizon.
func (h Horizon) Skips(createdAt time.Time) bool { return !createdAt.After(h.At) }
