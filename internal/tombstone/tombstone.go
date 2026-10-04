// Package tombstone marks a file that GitHub will never provide.
package tombstone

import "time"

type Reason string

const (
	Expired Reason = "expired"
	Deleted Reason = "deleted"
)

// Tombstone is stored as <target>.tombstone in place of target.
type Tombstone struct {
	LgFormat     int       `json:"lg_format"`
	TombstonedAt time.Time `json:"tombstoned_at"`
	Target       string    `json:"target"`
	HTTPStatus   *int      `json:"http_status"`
	Reason       Reason    `json:"reason"`
}

// New tombstones a target that lg never requested.
func New(target string, reason Reason) Tombstone {
	return Tombstone{LgFormat: 1, Target: target, Reason: reason}
}

func NotApplicable(target, url, message string, now time.Time) Tombstone { return Tombstone{} }

func FromError(err error, attemptUpdatedAt time.Time, logGrace time.Duration, now time.Time) (Tombstone, bool) {
	return Tombstone{}, false
}
