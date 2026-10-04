package mirror

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
)

// Discover lists the runs created in [from, to], newest first, halving the
// range while GitHub caps its listing.
func Discover(ctx context.Context, gh github.Client, from, to time.Time) ([]github.Run, error) {
	runs, total, err := gh.ListRuns(ctx, github.RunQuery{From: from, To: to, PerPage: 100})
	if err != nil || total < github.ListingCap {
		return runs, err
	}
	if to.Sub(from) < 2*time.Second {
		return nil, fmt.Errorf("%d runs were created in [%s, %s], and GitHub lists at most %d", total, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), github.ListingCap)
	}
	mid := from.Add(to.Sub(from) / 2).Truncate(time.Second)
	newer, err := Discover(ctx, gh, mid.Add(time.Second), to)
	if err != nil {
		return nil, err
	}
	older, err := Discover(ctx, gh, from, mid)
	if err != nil {
		return nil, err
	}
	return append(newer, older...), nil
}

// Merge joins listings into one, oldest first by created_at then id, keeping
// the first of a run that several list.
func Merge(listings ...[]github.Run) []github.Run {
	seen := map[int64]bool{}
	var merged []github.Run
	for _, listing := range listings {
		for _, run := range listing {
			if !seen[run.ID] {
				seen[run.ID] = true
				merged = append(merged, run)
			}
		}
	}
	slices.SortStableFunc(merged, func(a, b github.Run) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return merged
}

// nonTerminal are the statuses of a run that is not finished.
var nonTerminal = []string{"in_progress", "queued", "requested", "waiting", "pending", "action_required"}

// rerunWindow is how long GitHub allows re-running a run.
const rerunWindow = 30 * 24 * time.Hour

// RescanWindow is [now−min(30d, retention), now−backfill]: the runs a rerun
// can still change that the backfill window does not list. It is empty when
// the backfill window covers them.
func RescanWindow(now time.Time, backfill, retention time.Duration) (from, to time.Time, ok bool) {
	lookback := min(rerunWindow, retention)
	if backfill >= lookback {
		return time.Time{}, time.Time{}, false
	}
	return now.Add(-lookback), now.Add(-backfill), true
}

const rescanEvery = time.Hour

// rescan is state/rescan.json.
type rescan struct {
	RescannedAt time.Time `json:"rescanned_at"`
}

// discovery is what a cycle found to sync.
type discovery struct {
	// runs are oldest first.
	runs []listedRun
	// rescannedAt is when the rescan window was listed, and zero when it was not.
	rescannedAt time.Time
	// failed holds the errors that runScoped accepts.
	failed    []error
	discarded error
}

// discover lists the runs the cycle syncs: those created in the backfill
// window, those in a non-terminal status, hourly the runs on disk that a
// rerun could still change, and the watched runs and the runs with pending
// artifacts that no listing named.
func (m *Mirror) discover(ctx context.Context, gh github.Client, repo github.Repo, p *pending, w *watch) (discovery, error) {
	now := m.Clock.Now()
	listed, err := Discover(ctx, gh, now.Add(-m.Backfill), now)
	if err != nil {
		return discovery{}, err
	}
	for _, status := range nonTerminal {
		runs, _, err := gh.ListRuns(ctx, github.RunQuery{Status: status})
		if err != nil {
			return discovery{}, err
		}
		listed = append(listed, runs...)
	}
	rescanned, rescannedAt, discarded, err := m.rescan(ctx, gh, repo, now)
	if err != nil {
		return discovery{}, err
	}
	listed = append(listed, rescanned...)
	w.prune(now, m.Retention)
	watched, failed, err := m.fetchWatched(ctx, gh, w, listed)
	if err != nil {
		return discovery{}, err
	}
	listed = append(listed, watched...)
	if i := slices.IndexFunc(listed, func(run github.Run) bool { return !ofRepo(run, repo) }); i >= 0 {
		return discovery{}, fmt.Errorf("run %d belongs to %q, not %q", listed[i].ID, listed[i].Repository.FullName, repo.FullName)
	}
	d := discovery{rescannedAt: rescannedAt, failed: failed, discarded: discarded}
	for _, run := range Merge(listed, p.unlisted(listed, repo)) {
		dir, err := m.Store.FindRunDir(m.runDir(repo, run))
		if err != nil {
			return discovery{}, err
		}
		d.runs = append(d.runs, listedRun{Run: run, dir: dir})
	}
	return d, nil
}

func (m *Mirror) runDir(repo github.Repo, run github.Run) string {
	return layout.RunDir(layout.RepoDir(m.Store.Data(), m.Host, repo.FullName), run.Run)
}

func ofRepo(run github.Run, repo github.Repo) bool {
	return strings.EqualFold(run.Repository.FullName, repo.FullName)
}

// rescan lists the runs of RescanWindow that are on disk, an hour or more
// after state/rescan.json says it last did, so that a rerun of an older run
// that started and finished between two cycles gets its new attempts. A
// record from the future, after a clock step, does not delay it. Runs not
// on disk are left alone. The cycle records the rescan once it finishes.
func (m *Mirror) rescan(ctx context.Context, gh github.Client, repo github.Repo, now time.Time) (onDisk []github.Run, rescannedAt time.Time, discarded, err error) {
	from, to, ok := RescanWindow(now, m.Backfill, m.Retention)
	if !ok {
		return nil, time.Time{}, nil, nil
	}
	var last rescan
	discarded, err = newStateFile(m.Store, "rescan.json").read(func(raw []byte) error { return json.Unmarshal(raw, &last) })
	since := now.Sub(last.RescannedAt)
	if err != nil || (since >= 0 && since < rescanEvery) {
		return nil, time.Time{}, discarded, err
	}
	runs, err := Discover(ctx, gh, from, to)
	if err != nil {
		return nil, time.Time{}, discarded, err
	}
	for _, run := range runs {
		dir, err := m.Store.FindRunDir(m.runDir(repo, run))
		if err != nil {
			return nil, time.Time{}, discarded, err
		}
		has, err := m.Store.Has(dir)
		if err != nil {
			return nil, time.Time{}, discarded, err
		}
		if has {
			onDisk = append(onDisk, run)
		}
	}
	return onDisk, now, discarded, nil
}

// recordRescan writes state/rescan.json when the cycle rescanned.
func (m *Mirror) recordRescan(rescannedAt time.Time) error {
	if rescannedAt.IsZero() {
		return nil
	}
	return newStateFile(m.Store, "rescan.json").write(rescan{RescannedAt: rescannedAt.UTC().Truncate(time.Second)})
}

// fetchWatched gets each watched run that no listing named, since a run
// created before the backfill window appears in no listing once it
// completes. A run GitHub no longer has leaves the watch list. It returns
// the errors that runScoped accepts, leaving their runs watched.
func (m *Mirror) fetchWatched(ctx context.Context, gh github.Client, w *watch, listed []github.Run) (runs []github.Run, failed []error, err error) {
	for _, id := range slices.Sorted(maps.Keys(w.runs)) {
		if slices.ContainsFunc(listed, func(run github.Run) bool { return run.ID == id }) {
			delete(w.runs, id)
			continue
		}
		run, err := gh.GetRun(ctx, id)
		switch {
		case errors.Is(err, github.ErrNotFound):
			delete(w.runs, id)
		case runScoped(err):
			failed = append(failed, fmt.Errorf("run %d: %w", id, err))
		case err != nil:
			return nil, nil, err
		default:
			delete(w.runs, id)
			runs = append(runs, run)
		}
	}
	return runs, failed, nil
}
