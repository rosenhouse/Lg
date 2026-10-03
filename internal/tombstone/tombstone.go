// Package tombstone marks a file that GitHub will never provide.
package tombstone

type Reason string

const NotApplicable Reason = "not_applicable"

// Tombstone is stored as <target>.tombstone in place of target.
type Tombstone struct {
	LgFormat   int    `json:"lg_format"`
	Target     string `json:"target"`
	HTTPStatus *int   `json:"http_status"`
	Reason     Reason `json:"reason"`
}

// New tombstones a target that lg never requested.
func New(target string, reason Reason) Tombstone {
	return Tombstone{LgFormat: 1, Target: target, Reason: reason}
}
