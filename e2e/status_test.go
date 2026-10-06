package e2e_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg sync", Label("status"), func() {
	It("writes state/status.json with lg_format, cycle, last_sync_started_at, last_sync_finished_at, last_sync_ok_at, next_sync_at, sync_interval_seconds, blocked null, daemon_pid, daemon_version, and per repo default_branch, newest_completed_run_created_at, lag_seconds, runs, attempts, bytes_data, pending_units, retention_days, disk_cap_bytes and horizon", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		now := harness.DefaultNow()

		Expect(env.Sync()).To(gexec.Exit(0))

		st := env.Status()
		Expect(st).To(HaveKeyWithValue("lg_format", 1.0))
		Expect(st).To(HaveKeyWithValue("cycle", 1.0))
		for _, key := range []string{"last_sync_started_at", "last_sync_finished_at", "last_sync_ok_at"} {
			Expect(timeAt(st, key)).To(BeTemporally("~", now, harness.ExitTimeout), key)
		}
		Expect(st).To(HaveKeyWithValue("next_sync_at", BeNil()))
		Expect(st).To(HaveKeyWithValue("sync_interval_seconds", 600.0))
		Expect(st).To(HaveKeyWithValue("blocked", BeNil()))
		Expect(st).To(HaveKeyWithValue("daemon_pid", BeNil()))
		Expect(st).To(HaveKeyWithValue("daemon_version", BeNil()))
		Expect(st).To(HaveKey("repos"))
		Expect(st["repos"]).To(HaveKey("github.com/rosenhouse/lg"))
		repo := st["repos"].(map[string]any)["github.com/rosenhouse/lg"].(map[string]any)
		Expect(repo).To(HaveKeyWithValue("default_branch", "main"))
		newest := time.Date(2026, 10, 3, 14, 22, 54, 0, time.UTC)
		Expect(timeAt(repo, "newest_completed_run_created_at")).To(Equal(newest))
		Expect(repo).To(HaveKeyWithValue("lag_seconds", BeNumerically("~", now.Sub(newest).Seconds(), harness.ExitTimeout.Seconds())))
		Expect(repo).To(HaveKeyWithValue("runs", 1.0))
		Expect(repo).To(HaveKeyWithValue("attempts", 1.0))
		Expect(repo).To(HaveKeyWithValue("bytes_data", float64(treeBytes(env.Data()))))
		Expect(repo).To(HaveKeyWithValue("pending_units", 0.0))
		Expect(repo).To(HaveKeyWithValue("retention_days", 90.0))
		Expect(repo).To(HaveKeyWithValue("disk_cap_bytes", 50e9))
		Expect(repo).To(HaveKeyWithValue("horizon", BeNil()))

		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(env.Status()).To(HaveKeyWithValue("cycle", 2.0))
	})
})

var _ = Describe("blocked state", Label("status"), func() {
	It("keeps blocked.since across consecutive blocked cycles and clears blocked after a good cycle", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		first := harness.DefaultNow()
		env.GH().Fail("HTTP 401: Bad credentials")

		Expect(env.Sync()).To(gexec.Exit(3))
		Expect(env.Status()["blocked"]).To(HaveKeyWithValue("kind", "auth"))
		since := timeAt(env.Status()["blocked"].(map[string]any), "since")
		Expect(since).To(BeTemporally("~", first, harness.ExitTimeout))

		env.SetNow(first.Add(time.Hour), fake)
		Expect(env.Sync()).To(gexec.Exit(3))
		Expect(timeAt(env.Status(), "last_sync_started_at")).To(BeTemporally("~", first.Add(time.Hour), harness.ExitTimeout))
		Expect(timeAt(env.Status()["blocked"].(map[string]any), "since")).To(Equal(since))
		Expect(env.Status()).To(HaveKeyWithValue("last_sync_ok_at", BeNil()))

		env.GH().Restore()
		env.SetNow(first.Add(2*time.Hour), fake)
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(env.Status()).To(HaveKeyWithValue("blocked", BeNil()))
		Expect(timeAt(env.Status(), "last_sync_ok_at")).To(BeTemporally("~", first.Add(2*time.Hour), harness.ExitTimeout))
	})
})

var _ = Describe("blocked auth", Label("status"), func() {
	It("suggests `gh auth login --insecure-storage` when gh's stderr mentions the keyring", func() {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		env.GH().Fail("failed to read token from the Keyring: org.freedesktop.secrets was not provided by any .service files")

		session := env.Sync()

		Expect(session).To(gexec.Exit(3))
		hint := "gh auth login --hostname github.com --insecure-storage"
		Expect(string(session.Err.Contents())).To(ContainSubstring(hint))
		Expect(env.Status()["blocked"]).To(HaveKeyWithValue("detail", ContainSubstring(hint)))
	})
})

var _ = Describe("lg paths", Label("status"), func() {
	It("warns when last_sync_ok_at is older than twice sync_interval", func() {
		env := harness.New(lgPath)
		now := harness.DefaultNow()
		env.WriteStatus(statusJSON(now.Add(-19*time.Minute), "null"))
		fresh := env.Lg("paths")
		Eventually(fresh, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(fresh.Err.Contents()).To(BeEmpty())

		env.WriteStatus(statusJSON(now.Add(-25*time.Minute), "null"))
		stale := env.Lg("paths")
		Eventually(stale, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(stale.Err.Contents())).To(MatchRegexp(`^lg: warning: [^\n]*25m[^\n]*\n$`))
	})

	It("warns that the store was never synced when status.json is missing", func() {
		session := harness.New(lgPath).Lg("paths")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Err.Contents())).To(MatchRegexp(`^lg: warning: [^\n]*never synced[^\n]*\n$`))
	})
})

var _ = Describe("lg status", Label("status"), func() {
	It("prints last sync, next sync, lag, blocked state, pending units, horizon and whether a daemon holds the lock, and --json prints status.json", func() {
		env := harness.New(lgPath)
		raw := `{
  "lg_format": 1,
  "cycle": 7,
  "last_sync_started_at": "2026-10-03T17:59:00Z",
  "last_sync_finished_at": "2026-10-03T17:59:30Z",
  "last_sync_ok_at": "2026-10-03T17:50:00Z",
  "next_sync_at": "2026-10-03T18:01:00Z",
  "sync_interval_seconds": 600,
  "blocked": {"since": "2026-10-03T17:55:00Z", "kind": "rate_limit", "detail": "429 Too Many\n\u001b[1mRequests", "retry_at": "2026-10-03T18:01:00Z"},
  "daemon_pid": 4242,
  "daemon_version": "test",
  "repos": {
    "github.com/rosenhouse/lg": {
      "default_branch": "main",
      "newest_completed_run_created_at": "2026-10-03T14:22:54Z",
      "lag_seconds": 13026,
      "runs": 3,
      "attempts": 4,
      "bytes_data": 123456,
      "pending_units": 1,
      "pending": [{"run": 37129390741, "attempt": 2, "error": "502 Bad Gateway"}],
      "retention_days": 90,
      "disk_cap_bytes": 50000000000,
      "horizon": "2026-09-01T00:00:00Z"
    }
  }
}
`
		env.WriteStatus(raw)

		free := env.Lg("status")
		Eventually(free, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(free.Out.Contents())).To(And(ContainSubstring("next sync: none (daemon not running)\n"), ContainSubstring("daemon: not running")))

		Expect(os.MkdirAll(env.State(), 0o755)).To(Succeed())
		held, err := lock.Wait(filepath.Join(env.State(), "daemon.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		session := env.Lg("status")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal(`last sync: 2026-10-03T17:59:00Z, finished 2026-10-03T17:59:30Z (cycle 7)
last ok sync: 2026-10-03T17:50:00Z
next sync: 2026-10-03T18:01:00Z
blocked: rate_limit since 2026-10-03T17:55:00Z, retry_at 2026-10-03T18:01:00Z: 429 Too Many [1mRequests
daemon: running
github.com/rosenhouse/lg:
  default branch: main
  newest completed run: 2026-10-03T14:22:54Z, lag: 3h37m6s
  runs: 3, attempts: 4, bytes: 123456
  pending units: 1
    run 37129390741 attempt 2: 502 Bad Gateway
  horizon: 2026-09-01T00:00:00Z
  retention: 90 days, disk_cap: 50000000000 bytes
`))

		jsonOut := env.Lg("status", "--json")
		Eventually(jsonOut, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(jsonOut.Out.Contents())).To(Equal(raw))
	})
})

// statusJSON is a status.json from a good cycle that finished at ok, with
// sync_interval 10m and the given blocked.
func statusJSON(ok time.Time, blocked string) string {
	at := ok.UTC().Format(time.RFC3339)
	return `{"lg_format": 1, "cycle": 1, "last_sync_started_at": "` + at + `", "last_sync_finished_at": "` + at +
		`", "last_sync_ok_at": "` + at + `", "next_sync_at": null, "sync_interval_seconds": 600, "blocked": ` + blocked +
		`, "daemon_pid": null, "daemon_version": null, "repos": {}}` + "\n"
}

func timeAt(m map[string]any, key string) time.Time {
	GinkgoHelper()
	Expect(m).To(HaveKeyWithValue(key, BeAssignableToTypeOf("")))
	t, err := time.Parse(time.RFC3339, m[key].(string))
	Expect(err).NotTo(HaveOccurred())
	return t
}

// treeBytes sums the sizes of the regular files under dir.
func treeBytes(dir string) (n int64) {
	GinkgoHelper()
	Expect(filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		n += info.Size()
		return err
	})).To(Succeed())
	return n
}
