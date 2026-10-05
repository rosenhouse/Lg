// Package status is state/status.json, which says how fresh the store is.
package status

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/failure"
)

type Status struct {
	LgFormat            int             `json:"lg_format"`
	Cycle               int64           `json:"cycle"`
	LastSyncOKAt        *time.Time      `json:"last_sync_ok_at"`
	SyncIntervalSeconds int64           `json:"sync_interval_seconds"`
	Blocked             *Blocked        `json:"blocked"`
	Repos               map[string]Repo `json:"repos"`
}

type Blocked struct {
	Since   time.Time    `json:"since"`
	Kind    failure.Kind `json:"kind"`
	Detail  string       `json:"detail"`
	RetryAt *time.Time   `json:"retry_at"`
}

// Warning is the one line every command prints while st, the status at
// now, is blocked or stale, and "" otherwise (D24).
func Warning(now time.Time, st *Status) string {
	switch {
	case st == nil:
		return "never synced; run `lg sync`"
	case st.Blocked != nil:
		return st.Blocked.String()
	case st.LastSyncOKAt == nil:
		return "no sync has succeeded yet"
	}
	interval := time.Duration(st.SyncIntervalSeconds) * time.Second
	if age := now.Sub(*st.LastSyncOKAt); age > 2*interval {
		return fmt.Sprintf("last successful sync was %s ago, at %s, over twice sync_interval %s", age.Round(time.Second), st.LastSyncOKAt.Format(time.RFC3339), interval)
	}
	return ""
}

func (b Blocked) String() string {
	kind := string(b.Kind)
	if b.RetryAt != nil {
		kind += ", retry_at " + b.RetryAt.Format(time.RFC3339)
	}
	return fmt.Sprintf("sync blocked (%s) since %s: %s", kind, b.Since.Format(time.RFC3339), strings.Join(strings.Fields(b.Detail), " "))
}

type Repo struct {
	Runs int `json:"runs"`
}

// Read gives nil when path does not exist.
func Read(path string) (*Status, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &st, nil
}

// Write replaces path through a temp file, so readers see the old or the new file whole.
func Write(path string, st Status) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".status-*.json")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(raw, '\n'))
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}
