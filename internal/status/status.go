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
	"github.com/rosenhouse/lg/internal/retention"
)

type Status struct {
	LgFormat            int             `json:"lg_format"`
	Cycle               int64           `json:"cycle"`
	LastSyncStartedAt   time.Time       `json:"last_sync_started_at"`
	LastSyncFinishedAt  time.Time       `json:"last_sync_finished_at"`
	LastSyncOKAt        *time.Time      `json:"last_sync_ok_at"`
	NextSyncAt          *time.Time      `json:"next_sync_at"`
	SyncIntervalSeconds int64           `json:"sync_interval_seconds"`
	Blocked             *Blocked        `json:"blocked"`
	DaemonPID           *int            `json:"daemon_pid"`
	DaemonVersion       *string         `json:"daemon_version"`
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
	DefaultBranch               string     `json:"default_branch"`
	NewestCompletedRunCreatedAt *time.Time `json:"newest_completed_run_created_at"`
	LagSeconds                  *int64     `json:"lag_seconds"`
	Runs                        int        `json:"runs"`
	Attempts                    int        `json:"attempts"`
	BytesData                   int64      `json:"bytes_data"`
	PendingUnits                int        `json:"pending_units"`
	Pending                     []string   `json:"pending"`
	RetentionDays               int64      `json:"retention_days"`
	DiskCapBytes                int64      `json:"disk_cap_bytes"`
	Horizon                     *time.Time `json:"horizon"`
}

type Cycle struct {
	Started, Finished time.Time
	Err               error
	// Completed is whether the cycle ran to its end.
	Completed bool
	// Pending are the units the cycle left for the next one, one error each.
	Pending       []error
	Repo          string
	DefaultBranch string
	SyncInterval  time.Duration
	Retention     time.Duration
	DiskCap       int64
	Disk          Disk
}

type Disk struct {
	Runs, Attempts           int
	Bytes                    int64
	NewestCompleted, Horizon time.Time
}

// Next is the status after cycle c, given the status before it, prev,
// which is nil when there is none.
func Next(prev *Status, c Cycle) Status {
	finished := c.Finished.UTC().Truncate(time.Second)
	st := Status{
		LgFormat:            1,
		Cycle:               1,
		LastSyncStartedAt:   c.Started.UTC().Truncate(time.Second),
		LastSyncFinishedAt:  finished,
		SyncIntervalSeconds: int64(c.SyncInterval / time.Second),
	}
	repo := Repo{
		DefaultBranch: c.DefaultBranch,
		Runs:          c.Disk.Runs,
		Attempts:      c.Disk.Attempts,
		BytesData:     c.Disk.Bytes,
		RetentionDays: int64(c.Retention / (24 * time.Hour)),
		DiskCapBytes:  c.DiskCap,
		Horizon:       timeOrNil(c.Disk.Horizon),
	}
	if !c.Disk.NewestCompleted.IsZero() {
		repo.NewestCompletedRunCreatedAt = timeOrNil(c.Disk.NewestCompleted)
		lag := int64(finished.Sub(c.Disk.NewestCompleted) / time.Second)
		repo.LagSeconds = &lag
	}
	var last Repo
	if prev != nil {
		st.Cycle = prev.Cycle + 1
		st.LastSyncOKAt = prev.LastSyncOKAt
		last = prev.Repos[c.Repo]
	}
	if repo.DefaultBranch == "" {
		repo.DefaultBranch = last.DefaultBranch
	}
	if c.Completed {
		st.LastSyncOKAt = &finished
		repo.Pending = lines(c.Pending)
	} else {
		repo.Pending = append([]string{}, last.Pending...)
		st.Blocked = nextBlocked(prev, c.Err, st.LastSyncStartedAt)
	}
	repo.PendingUnits = len(repo.Pending)
	st.Repos = map[string]Repo{c.Repo: repo}
	return st
}

// nextBlocked is what err blocks, since prev's blocked.since when prev was
// blocked too, else since started. An err that blocks nothing keeps prev's
// blocked.
func nextBlocked(prev *Status, err error, started time.Time) *Blocked {
	var last *Blocked
	if prev != nil {
		last = prev.Blocked
	}
	var b failure.Blocked
	if !errors.As(err, &b) {
		return last
	}
	since := started
	if last != nil {
		since = last.Since
	}
	return &Blocked{Since: since, Kind: b.Kind, Detail: b.Detail, RetryAt: timeOrNil(b.RetryAt)}
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// lines gives the message of each error on one line.
func lines(errs []error) []string {
	found := []string{}
	for _, err := range errs {
		found = append(found, strings.Join(strings.Fields(err.Error()), " "))
	}
	return found
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

// Measure finds the runs of repo, as <host>/<owner>/<name> in any case, under
// data/, and its eviction horizon in state/. A completed run is one with an
// attempt on disk.
func Measure(data, state, repo string) (Disk, error) {
	runs, err := retention.Scan(data)
	if err != nil {
		return Disk{}, err
	}
	horizons, err := retention.PeekHorizons(state)
	if err != nil {
		return Disk{}, err
	}
	var d Disk
	for _, r := range runs {
		if !strings.EqualFold(r.Repo, repo) {
			continue
		}
		d.Runs++
		d.Attempts += r.Attempts
		d.Bytes += r.Bytes
		if r.Attempts > 0 && r.CreatedAt.After(d.NewestCompleted) {
			d.NewestCompleted = r.CreatedAt
		}
	}
	for key, horizon := range horizons {
		if strings.EqualFold(key, repo) && horizon.After(d.Horizon) {
			d.Horizon = horizon
		}
	}
	return d, nil
}
