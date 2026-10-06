// Package status is state/status.json, which says how fresh the store is.
package status

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/retention"
	"github.com/rosenhouse/lg/internal/store"
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
	ServedRequest       int64           `json:"served_request"`
	ConfigError         *string         `json:"config_error"`
	LastSyncErrors      []string        `json:"last_sync_errors"`
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
		return "never synced"
	case st.Blocked != nil:
		return "sync blocked: " + st.Blocked.String()
	case st.LastSyncOKAt == nil:
		return "no sync has succeeded yet"
	}
	interval := time.Duration(st.SyncIntervalSeconds) * time.Second
	if age := now.Sub(*st.LastSyncOKAt); age/2 > interval {
		return fmt.Sprintf("last successful sync was %s ago, at %s, over twice sync_interval %s", age.Round(time.Second), st.LastSyncOKAt.Format(time.RFC3339), interval)
	}
	return ""
}

// String gives b on one line, without the terminal controls that gh's
// stderr can put in its detail.
func (b Blocked) String() string {
	s := fmt.Sprintf("%s since %s", b.Kind, b.Since.Format(time.RFC3339))
	if b.RetryAt != nil {
		s += ", retry_at " + b.RetryAt.Format(time.RFC3339)
	}
	return s + ": " + OneLine(b.Detail)
}

// OneLine gives s on one line, without terminal controls.
func OneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

type Repo struct {
	DefaultBranch               string     `json:"default_branch"`
	NewestCompletedRunCreatedAt *time.Time `json:"newest_completed_run_created_at"`
	LagSeconds                  *int64     `json:"lag_seconds"`
	Runs                        int        `json:"runs"`
	Attempts                    int        `json:"attempts"`
	BytesData                   int64      `json:"bytes_data"`
	PendingUnits                int        `json:"pending_units"`
	Pending                     []Pending  `json:"pending"`
	RetentionDays               int64      `json:"retention_days"`
	DiskCapBytes                int64      `json:"disk_cap_bytes"`
	Horizon                     *time.Time `json:"horizon"`
}

// Unit is a run, or an attempt or artifact of it.
type Unit struct {
	Run      int64 `json:"run"`
	Attempt  int   `json:"attempt,omitempty"`
	Artifact int64 `json:"artifact,omitempty"`
}

func (u Unit) String() string {
	switch {
	case u.Attempt != 0:
		return fmt.Sprintf("run %d attempt %d", u.Run, u.Attempt)
	case u.Artifact != 0:
		return fmt.Sprintf("run %d artifact %d", u.Run, u.Artifact)
	}
	return fmt.Sprintf("run %d", u.Run)
}

// Pending is a unit that a cycle left for the next one, with its last error.
type Pending struct {
	Unit
	Error string `json:"error"`
}

func (p Pending) String() string { return p.Unit.String() + ": " + p.Error }

type Cycle struct {
	Started, Finished time.Time
	Err               error
	// Completed is whether the cycle and its retention ran to their end.
	Completed bool
	// Pending are the units the cycle left for the next one, up to where it
	// stopped.
	Pending       []Pending
	Repo          string
	DefaultBranch string
	SyncInterval  time.Duration
	Retention     time.Duration
	DiskCap       int64
	Disk          Disk
	Daemon        *Daemon
	NextSyncAt    time.Time
	ServedRequest int64
	ConfigError   error
}

type Daemon struct {
	PID     int
	Version string
}

type Disk struct {
	Runs, Attempts           int
	Bytes                    int64
	NewestCompleted, Horizon time.Time
	// Published are the attempts and artifacts on disk.
	Published map[Unit]bool
}

const day = 24 * time.Hour

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
		LastSyncErrors:      lines(c.Err),
	}
	if !errors.Is(c.Err, context.Canceled) {
		st.NextSyncAt = timeOrNil(c.NextSyncAt)
		st.ServedRequest = c.ServedRequest
	}
	if c.Daemon != nil {
		d := *c.Daemon
		st.DaemonPID, st.DaemonVersion = &d.PID, &d.Version
	}
	if c.ConfigError != nil {
		msg := OneLine(c.ConfigError.Error())
		st.ConfigError = &msg
	}
	repo := Repo{DefaultBranch: c.DefaultBranch}
	repo.setDisk(c.Disk, finished, c.Retention, c.DiskCap)
	var last Repo
	if prev != nil {
		st.Cycle = prev.Cycle + 1
		st.LastSyncOKAt = prev.LastSyncOKAt
		st.ServedRequest = max(st.ServedRequest, prev.ServedRequest)
		last = prev.Repos[c.Repo]
	}
	if repo.DefaultBranch == "" {
		repo.DefaultBranch = last.DefaultBranch
	}
	repo.Pending = oneLineErrors(c.Pending)
	if c.Completed {
		st.LastSyncOKAt = &finished
	} else {
		repo.Pending = append(repo.Pending, carried(last.Pending, repo.Pending, c.Disk.Published)...)
		st.Blocked = nextBlocked(prev, c.Err, st.LastSyncStartedAt)
	}
	repo.PendingUnits = len(repo.Pending)
	st.Repos = map[string]Repo{c.Repo: repo}
	return st
}

// Remeasured is st with repo's disk fields from d, retention and diskCap,
// and its lag as at st's last sync.
func Remeasured(st Status, repo string, d Disk, retention time.Duration, diskCap int64) Status {
	st.Repos = maps.Clone(st.Repos)
	r := st.Repos[repo]
	r.setDisk(d, st.LastSyncFinishedAt, retention, diskCap)
	st.Repos[repo] = r
	return st
}

// setDisk sets r's fields that d, retention and diskCap give, with its lag
// as at synced.
func (r *Repo) setDisk(d Disk, synced time.Time, retention time.Duration, diskCap int64) {
	r.Runs, r.Attempts, r.BytesData = d.Runs, d.Attempts, d.Bytes
	r.RetentionDays = int64((retention + day - 1) / day)
	r.DiskCapBytes = diskCap
	r.Horizon = timeOrNil(d.Horizon)
	r.NewestCompletedRunCreatedAt, r.LagSeconds = nil, nil
	if !d.NewestCompleted.IsZero() {
		r.NewestCompletedRunCreatedAt = timeOrNil(d.NewestCompleted)
		lag := int64(synced.Sub(d.NewestCompleted) / time.Second)
		r.LagSeconds = &lag
	}
}

// nextBlocked is what err blocks, since prev's blocked.since when prev was
// blocked too, else since started. A cancelled cycle learned nothing, so it
// keeps prev's blocked.
func nextBlocked(prev *Status, err error, started time.Time) *Blocked {
	var last *Blocked
	if prev != nil {
		last = prev.Blocked
	}
	var b failure.Blocked
	if !errors.As(err, &b) {
		if errors.Is(err, context.Canceled) {
			return last
		}
		return nil
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

// lines gives each line of err passed through OneLine.
func lines(err error) []string {
	found := []string{}
	if err == nil {
		return found
	}
	for _, line := range strings.Split(err.Error(), "\n") {
		found = append(found, OneLine(line))
	}
	return found
}

// oneLineErrors gives pending with each error passed through OneLine.
func oneLineErrors(pending []Pending) []Pending {
	found := []Pending{}
	for _, p := range pending {
		found = append(found, Pending{Unit: p.Unit, Error: OneLine(p.Error)})
	}
	return found
}

// carried gives the units of last that a cycle which stopped early neither
// left again, in now, nor published.
func carried(last, now []Pending, published map[Unit]bool) []Pending {
	var kept []Pending
	for _, p := range last {
		if !published[p.Unit] && !slices.ContainsFunc(now, func(n Pending) bool { return n.Unit == p.Unit }) {
			kept = append(kept, p)
		}
	}
	return kept
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

// Write replaces path through fsys, so readers see the old or the new file
// whole. It writes <, > and & as they are, so rg finds them.
func Write(fsys store.FS, path string, st Status) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		return err
	}
	return store.ReplaceFileFS(fsys, path, buf.Bytes())
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
	d := Disk{Published: map[Unit]bool{}}
	for _, r := range runs {
		if !strings.EqualFold(r.Repo, repo) {
			continue
		}
		for _, n := range r.Attempts {
			d.Published[Unit{Run: r.ID, Attempt: n}] = true
		}
		for _, id := range r.Artifacts {
			d.Published[Unit{Run: r.ID, Artifact: id}] = true
		}
		d.Runs++
		d.Attempts += len(r.Attempts)
		d.Bytes += r.Bytes
		if len(r.Attempts) > 0 && r.CreatedAt.After(d.NewestCompleted) {
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
