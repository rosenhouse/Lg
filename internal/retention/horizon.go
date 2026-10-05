package retention

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/store"
)

// Horizons is state/horizon.json. It maps each repo dir, as
// <host>/<owner>/<repo> below data/, to the newest created_at of a run of it
// that disk_cap evicted. Losing it costs one re-download of the evicted runs.
type Horizons map[string]time.Time

const horizonFile = "horizon.json"

// ReadHorizons gives no horizons, which skip nothing, when the file is
// missing. One that does not parse is moved aside, and ReadHorizons gives no
// horizons and the parse error as discarded.
func ReadHorizons(s *store.Store) (h Horizons, discarded, err error) {
	discarded, err = s.ReadState(horizonFile, func(raw []byte) error { return json.Unmarshal(raw, &h) })
	if discarded != nil || err != nil {
		return nil, discarded, err
	}
	return h, nil, nil
}

// PeekHorizons reads the horizons in state without the write lock. It leaves
// a corrupt file for ReadHorizons to move aside, and skips nothing for it, as
// ReadHorizons does.
func PeekHorizons(state string) (Horizons, error) {
	raw, err := os.ReadFile(filepath.Join(state, horizonFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h Horizons
	if json.Unmarshal(raw, &h) != nil {
		return nil, nil
	}
	return h, nil
}

func (h Horizons) Write(s *store.Store) error { return s.WriteState(horizonFile, h) }

// Skips reports whether a run of repo created at createdAt is at or before
// repo's horizon.
func (h Horizons) Skips(repo string, createdAt time.Time) bool {
	at, ok := h[repo]
	return ok && !createdAt.After(at)
}

// RepoKey names a repo dir in Horizons.
func RepoKey(host, fullName string) string {
	return filepath.ToSlash(layout.RepoDir("", host, fullName))
}
