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

// ReadHorizon gives the zero Horizon, which skips nothing, when the file is
// missing. One that does not parse is moved aside, and ReadHorizon gives the
// zero Horizon and the parse error as discarded.
func ReadHorizon(s *store.Store) (h Horizon, discarded, err error) {
	path := horizonFile(s)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Horizon{}, nil, nil
	}
	if err != nil {
		return Horizon{}, nil, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		aside := path + ".corrupt"
		if err := s.Rename(path, aside); err != nil {
			return Horizon{}, nil, err
		}
		return Horizon{}, &CorruptError{Path: path, Err: fmt.Errorf("moved to %s: %w", aside, err)}, nil
	}
	return h, nil, nil
}

// CorruptError is a state/horizon.json that did not parse.
type CorruptError struct {
	Path string
	Err  error
}

func (e *CorruptError) Error() string { return e.Path + ": " + e.Err.Error() }

func (e *CorruptError) Unwrap() error { return e.Err }

func (h Horizon) Write(s *store.Store) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return s.ReplaceFile(horizonFile(s), append(raw, '\n'))
}

// Skips reports whether a run created at createdAt is at or before the horizon.
func (h Horizon) Skips(createdAt time.Time) bool { return !createdAt.After(h.At) }
