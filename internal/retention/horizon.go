package retention

import (
	"time"

	"github.com/rosenhouse/lg/internal/store"
)

// Horizon is state/horizon.json: the newest created_at of a run that
// disk_cap evicted.
type Horizon struct {
	At time.Time `json:"horizon"`
}

func ReadHorizon(s *store.Store) (Horizon, error) { return Horizon{}, nil }

func (h Horizon) Write(s *store.Store) error { return nil }

func (h Horizon) Skips(createdAt time.Time) bool { return false }
