package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/version"
)

// near matches an RFC 3339 time within harness.ExitTimeout of want.
func near(want time.Time) OmegaMatcher {
	return WithTransform(func(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) },
		BeTemporally("~", want, harness.ExitTimeout))
}

type failingRunner struct{ stderr string }

func (f failingRunner) Run(context.Context, string, []string, map[string]string) (stdout, stderr []byte, err error) {
	return nil, []byte(f.stderr), errors.New("exit status 1")
}

var _ = DescribeTable("lg sync records blocked.kind and still writes status.json", Label("status"),
	func(setup func(*harness.CLI), kind string, retryAfter time.Duration) {
		s := harness.NewCLI()
		setup(s)

		Expect(s.Main("sync")).To(Equal(3))

		blocked := s.Status()["blocked"]
		Expect(blocked).To(HaveKeyWithValue("kind", kind))
		Expect(blocked).To(HaveKeyWithValue("since", near(harness.DefaultNow())))
		Expect(blocked).To(HaveKeyWithValue("detail", Not(BeEmpty())))
		if retryAfter == 0 {
			Expect(blocked).To(HaveKeyWithValue("retry_at", BeNil()))
		} else {
			Expect(blocked).To(HaveKeyWithValue("retry_at", near(harness.DefaultNow().Add(retryAfter))))
		}
	},
	Entry("auth when gh fails", func(s *harness.CLI) { s.Runner = failingRunner{"not logged in"} }, "auth", time.Duration(0)),
	Entry("auth on 401", func(s *harness.CLI) { s.Fake.RequireToken("another-token") }, "auth", time.Duration(0)),
	Entry("rate_limit with retry_at", func(s *harness.CLI) {
		s.Fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "120"}})
	}, "rate_limit", 2*time.Minute),
	Entry("unreachable", func(s *harness.CLI) { s.Fake.Close() }, "unreachable", time.Duration(0)),
	Entry("local_io", func(s *harness.CLI) { s.FS.FailOnUnder("write", filepath.Join(s.Home, "tmp"), syscall.ENOSPC) }, "local_io", time.Duration(0)),
)

var _ = DescribeTable("cli.Main prints one warning line on stderr while blocked, leaving stdout unchanged", Label("status"),
	func(args []string, wantStdout func(s *harness.CLI) string) {
		s := harness.NewCLI()
		Expect(s.Main("sync")).To(Equal(0))
		st := s.Status()
		st["blocked"] = map[string]any{"since": harness.DefaultNow().Format(time.RFC3339), "kind": "auth", "detail": "HTTP 401: Bad credentials\n(see gh auth status)", "retry_at": nil}
		raw, err := json.Marshal(st)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(s.StatusFile(), raw, 0o644)).To(Succeed())

		Expect(s.Main(args...)).To(Equal(0))

		Expect(s.Stdout.String()).To(Equal(wantStdout(s)))
		Expect(strings.Count(s.Stderr.String(), "\n")).To(Equal(1), s.Stderr.String())
		Expect(s.Stderr.String()).To(MatchRegexp(`^lg: warning: .*blocked.*auth.*HTTP 401: Bad credentials`))
	},
	Entry("paths", []string{"paths"}, func(s *harness.CLI) string {
		logs, err := filepath.Glob(filepath.Join(s.Home, "data/*/*/*/runs/*/*/attempt-*/jobs/*/log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(10))
		return strings.Join(logs, "\n") + "\n"
	}),
	Entry("status", []string{"status", "--json"}, func(s *harness.CLI) string {
		raw, err := os.ReadFile(s.StatusFile())
		Expect(err).NotTo(HaveOccurred())
		return string(raw)
	}),
	Entry("version", []string{"version"}, func(*harness.CLI) string { return version.Version + "\n" }),
)

var _ = DescribeTable("cli.Main exit codes", Label("status"),
	func(code int, setups ...func(*harness.CLI) []string) {
		for _, setup := range setups {
			s := harness.NewCLI()
			Expect(s.Main(setup(s)...)).To(Equal(code), s.Stderr.String())
		}
	},
	Entry("0 on success", 0, func(*harness.CLI) []string { return []string{"sync"} }),
	Entry("1 with pending units", 1, func(s *harness.CLI) []string {
		s.Fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusBadGateway})
		return []string{"sync"}
	}),
	Entry("2 on usage or config errors", 2,
		func(*harness.CLI) []string { return []string{"sync", "--no-such-flag"} },
		func(s *harness.CLI) []string {
			s.WriteConfig("repo: rosenhouse/lg\nsync_interval: 1s\n")
			return []string{"sync"}
		}),
	Entry("3 when blocked", 3, func(s *harness.CLI) []string {
		s.Runner = failingRunner{"not logged in"}
		return []string{"sync"}
	}),
)

var _ = Describe("lg status before any sync", Label("status"), func() {
	It("prints that it never synced, and with --json exits 1 naming state/status.json", func() {
		s := harness.NewCLI()

		Expect(s.Main("status")).To(Equal(0))
		Expect(s.Stdout.String()).To(Equal("last sync: never\ndaemon: not running\n"))

		Expect(s.Main("status", "--json")).To(Equal(1))
		Expect(s.Stdout.String()).To(BeEmpty())
		Expect(s.Stderr.String()).To(Equal("lg: warning: never synced; run `lg sync`\nlg: " + s.StatusFile() + " does not exist\n"))
	})
})

var _ = Describe("lg status after a good sync", Label("status"), func() {
	It("prints every field of a status.json with nothing blocked, scheduled or running", func() {
		s := harness.NewCLI()
		s.WriteStatus(goodStatus)

		Expect(s.Main("status")).To(Equal(0))
		Expect(s.Stdout.String()).To(Equal(`last sync: 2026-10-03T17:59:00Z, finished 2026-10-03T17:59:30Z (cycle 2)
last ok sync: 2026-10-03T17:59:30Z
next sync: none (daemon not running)
blocked: no
daemon: not running
github.com/rosenhouse/lg:
  default branch: main
  newest completed run created: none, lag: none
  runs: 0, attempts: 0, bytes: 0
  pending units: 0
  horizon: none
  retention: 30 days, disk_cap: 1MB
`))
	})
})

var _ = Describe("lg status with a daemon running and no next_sync_at", Label("status"), func() {
	It("prints that no next sync is scheduled", func() {
		s := harness.NewCLI()
		s.WriteStatus(goodStatus)
		held, err := lock.Wait(filepath.Join(s.Home, "state", "daemon.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		Expect(s.Main("status")).To(Equal(0))
		Expect(s.Stdout.String()).To(ContainSubstring("next sync: none scheduled\n"))
	})
})

var _ = DescribeTable("cli.Main with a unit stuck pending", Label("status"),
	func(command, warning string) {
		s := harness.NewCLI()
		s.WriteStatus(strings.Replace(goodStatus, `"pending": []`,
			`"pending": [{"run": 1, "attempt": 2, "error": "503", "since": "2026-10-03T17:30:00Z"}]`, 1))

		Expect(s.Main(command)).To(Equal(0))
		Expect(s.Stderr.String()).To(Equal(warning))
	},
	Entry("tells to run lg status", "version", "lg: warning: 1 units pending since 2026-10-03T17:30:00Z; run `lg status`\n"),
	Entry("does not tell lg status to run itself", "status", "lg: warning: 1 units pending since 2026-10-03T17:30:00Z\n"),
)

// goodStatus is a status.json from a one-shot sync with no runs.
const goodStatus = `{
  "lg_format": 1,
  "cycle": 2,
  "last_sync_started_at": "2026-10-03T17:59:00Z",
  "last_sync_finished_at": "2026-10-03T17:59:30Z",
  "last_sync_ok_at": "2026-10-03T17:59:30Z",
  "next_sync_at": null,
  "sync_interval_seconds": 600,
  "blocked": null,
  "daemon_pid": null,
  "daemon_version": null,
  "repos": {
    "github.com/rosenhouse/lg": {
      "default_branch": "main",
      "newest_completed_run_created_at": null,
      "lag_seconds": null,
      "runs": 0,
      "attempts": 0,
      "bytes_data": 0,
      "pending_units": 0,
      "pending": [],
      "retention_days": 30,
      "disk_cap_bytes": 1000000,
      "horizon": null
    }
  }
}
`

var _ = DescribeTable("cli.Main on a store that never synced", Label("status"),
	func(command, warning string) {
		s := harness.NewCLI()

		Expect(s.Main(command)).To(Equal(0))
		Expect(s.Stderr.String()).To(Equal(warning))
	},
	Entry("tells to run lg sync", "paths", "lg: warning: never synced; run `lg sync`\n"),
	Entry("does not tell lg sync to run itself", "sync", "lg: warning: never synced\n"),
)

var _ = DescribeTable("cli.Main before lg init", Label("status"),
	func(args []string, warning string) {
		s := harness.NewCLI()
		Expect(os.Remove(s.Config)).To(Succeed())

		s.Main(args...)
		Expect(s.Stderr.String()).To(HavePrefix(warning))
	},
	Entry("tells to run lg init", []string{"version"}, "lg: warning: never synced; run `lg init --repo OWNER/NAME`\n"),
)

var _ = Describe("lg init", Label("status"), func() {
	It("names the config it wrote and tells to run lg sync next, without a warning", func() {
		s := harness.NewCLI()
		Expect(os.Remove(s.Config)).To(Succeed())

		Expect(s.Main("init", "--repo", "o/r")).To(Equal(0))
		Expect(s.Stdout.String()).To(Equal("wrote " + s.Config + "; run `lg sync` next\n"))
		Expect(s.Stderr.String()).To(BeEmpty())
	})

	It("prints only its error when it fails", func() {
		s := harness.NewCLI()

		Expect(s.Main("init", "--repo", "o/r")).To(Equal(2))
		Expect(s.Stdout.String()).To(BeEmpty())
		Expect(s.Stderr.String()).To(Equal("lg: " + s.Config + " already exists\n"))
	})
})

var _ = Describe("lg sync with a pending unit", Label("status"), func() {
	It("records the unit with its error in status.json, and the sync as ok", func() {
		s := harness.NewCLI()
		s.Fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusBadGateway})

		Expect(s.Main("sync")).To(Equal(1))

		st := s.Status()
		Expect(st).To(HaveKeyWithValue("last_sync_ok_at", near(harness.DefaultNow())))
		repo := st["repos"].(map[string]any)["github.com/rosenhouse/lg"]
		Expect(repo).To(HaveKeyWithValue("pending_units", 1.0))
		Expect(repo).To(HaveKeyWithValue("pending", ConsistOf(And(
			HaveKeyWithValue("run", 37129390741.0),
			HaveKeyWithValue("attempt", 1.0),
			HaveKeyWithValue("error", ContainSubstring("502 Bad Gateway"))))))
	})
})

var _ = DescribeTable("lg sync that discards a corrupt hint file", Label("status"),
	func(name string) {
		s := harness.NewCLI()
		Expect(s.Main("sync")).To(Equal(0))
		Expect(os.WriteFile(filepath.Join(s.Home, "state", name), []byte("{"), 0o644)).To(Succeed())

		Expect(s.Main("sync")).To(Equal(1))
		Expect(s.Stderr.String()).To(ContainSubstring(name + ".corrupt"))

		repo := s.Status()["repos"].(map[string]any)["github.com/rosenhouse/lg"]
		Expect(repo).To(HaveKeyWithValue("pending_units", 0.0))
		Expect(repo).To(HaveKeyWithValue("pending", BeEmpty()))
	},
	Entry("watch.json", "watch.json"),
	Entry("pending-artifacts.json", "pending-artifacts.json"),
	Entry("rescan.json", "rescan.json"),
	Entry("listed.json", "listed.json"),
)

var _ = Describe("lg sync with a watched run GitHub fails to serve", Label("status"), func() {
	It("records the run as pending", func() {
		s := harness.NewCLI()
		Expect(s.Main("sync")).To(Equal(0))
		Expect(os.WriteFile(filepath.Join(s.Home, "state", "watch.json"), []byte(`{"github.com": {"42": {"created_at": "2026-10-03T17:00:00Z"}}}`), 0o644)).To(Succeed())
		s.Fake.Fail("api", "/actions/runs/42", fakegithub.Fault{Status: http.StatusBadGateway})

		Expect(s.Main("sync")).To(Equal(1))

		repo := s.Status()["repos"].(map[string]any)["github.com/rosenhouse/lg"]
		Expect(repo).To(HaveKeyWithValue("pending", ConsistOf(And(
			HaveKeyWithValue("run", 42.0),
			Not(HaveKey("attempt")),
			HaveKeyWithValue("error", ContainSubstring("502 Bad Gateway"))))))
		Expect(s.Stderr.String()).To(ContainSubstring("run 42: "))
	})
})

var _ = Describe("cli.Main with a status.json it cannot parse", Label("status"), func() {
	It("warns naming the file and runs the command", func() {
		s := harness.NewCLI()
		Expect(s.Main("sync")).To(Equal(0))
		Expect(os.WriteFile(s.StatusFile(), []byte("{"), 0o644)).To(Succeed())

		Expect(s.Main("version")).To(Equal(0))
		Expect(s.Stdout.String()).To(Equal(version.Version + "\n"))
		Expect(s.Stderr.String()).To(MatchRegexp(`^lg: warning: ` + regexp.QuoteMeta(s.StatusFile()) + `: [^\n]+\n$`))
	})

	It("replaces it after a good sync, which exits 0", func() {
		s := harness.NewCLI()
		Expect(s.Main("sync")).To(Equal(0))
		Expect(os.WriteFile(s.StatusFile(), []byte(`{"cycle": 5,`), 0o644)).To(Succeed())

		Expect(s.Main("sync")).To(Equal(0), s.Stderr.String())
		Expect(s.Status()).To(HaveKeyWithValue("cycle", 1.0))
	})
})

var _ = DescribeTable("lg sync that GitHub stops before the cycle ends", Label("status"),
	func(match string) {
		s := harness.NewCLI()
		s.Fake.Fail("api", match, fakegithub.Fault{Status: http.StatusBadGateway})

		Expect(s.Main("sync")).To(Equal(1))

		st := s.Status()
		Expect(st).To(HaveKeyWithValue("last_sync_ok_at", BeNil()))
		Expect(st["repos"]).To(HaveKeyWithValue("github.com/rosenhouse/lg", HaveKeyWithValue("pending", BeEmpty())))
		Expect(s.Main("paths")).To(Equal(0))
		Expect(s.Stderr.String()).To(Equal("lg: warning: no sync has succeeded yet\n"))
	},
	Entry("on GET /repos", "/repos/rosenhouse/lg"),
	Entry("on the runs listing", "/actions/runs"),
)

var _ = Describe("lg sync that cannot write status.json", Label("status"), func() {
	It("exits 3, blocked as local_io, naming status.json", func() {
		s := harness.NewCLI()
		s.FS.FailOnUnder("rename", s.StatusFile(), syscall.ENOSPC)

		Expect(s.Main("sync")).To(Equal(3))

		Expect(s.Stderr.String()).To(ContainSubstring("blocked (local_io)"))
		Expect(s.Stderr.String()).To(ContainSubstring("status.json"))
		Expect(s.StatusFile()).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("lg status after a cycle blocked before GET /repos", Label("status"), func() {
	It("prints the default branch as unknown", func() {
		s := harness.NewCLI()
		s.Runner = failingRunner{"not logged in"}
		Expect(s.Main("sync")).To(Equal(3))

		Expect(s.Main("status")).To(Equal(0))
		Expect(s.Stdout.String()).To(ContainSubstring("\n  default branch: unknown\n"))
	})
})

var _ = Describe("lg status with several repos", Label("status"), func() {
	It("prints them in name order", func() {
		s := harness.NewCLI()
		names := []string{"github.com/o/h", "github.com/o/c", "github.com/o/f", "github.com/o/a", "github.com/o/g", "github.com/o/b", "github.com/o/e", "github.com/o/d"}
		repos := map[string]any{}
		for _, name := range names {
			repos[name] = map[string]any{"default_branch": "main"}
		}
		raw, err := json.Marshal(map[string]any{"lg_format": 1, "sync_interval_seconds": 600, "repos": repos})
		Expect(err).NotTo(HaveOccurred())
		s.WriteStatus(string(raw))

		for range 5 {
			Expect(s.Main("status")).To(Equal(0))
			Expect(regexp.MustCompile(`(?m)^github\.com/o/.:$`).FindAllString(s.Stdout.String(), -1)).To(Equal([]string{
				"github.com/o/a:", "github.com/o/b:", "github.com/o/c:", "github.com/o/d:", "github.com/o/e:", "github.com/o/f:", "github.com/o/g:", "github.com/o/h:",
			}))
		}
	})
})

var _ = Describe("lg status after a daemon rejected config.yaml", Label("status"), func() {
	It("prints the config error", func() {
		s := harness.NewCLI()
		s.WriteStatus(`{"lg_format": 1, "sync_interval_seconds": 600, "config_error": "config.yaml: sync_interval must be at least 1m: 30s; kept the last good config"}`)

		Expect(s.Main("status")).To(Equal(0))
		Expect(s.Stdout.String()).To(ContainSubstring("\nblocked: no\nconfig error: config.yaml: sync_interval must be at least 1m: 30s; kept the last good config\ndaemon: not running\n"))
	})
})

var _ = Describe("cli.Main after a daemon rejected config.yaml", Label("status"), func() {
	It("warns 'config.yaml is invalid: ...'", func() {
		s := harness.NewCLI()
		s.WriteStatus(strings.Replace(goodStatus, `"daemon_version": null,`,
			`"daemon_version": null, "config_error": "config.yaml: sync_interval must be at least 1m: 30s; kept the last good config",`, 1))

		Expect(s.Main("version")).To(Equal(0))
		Expect(s.Stderr.String()).To(Equal("lg: warning: config.yaml is invalid: config.yaml: sync_interval must be at least 1m: 30s; kept the last good config\n"))
	})
})

var _ = Describe("lg status with a status.json it cannot parse", Label("status"), func() {
	It("exits 1, naming the file once", func() {
		s := harness.NewCLI()
		s.WriteStatus("{")

		Expect(s.Main("status")).To(Equal(1))
		Expect(s.Stdout.String()).To(BeEmpty())
		Expect(s.Stderr.String()).To(Equal("lg: warning: " + s.StatusFile() + ": unexpected end of JSON input\n"))
	})
})

var _ = Describe("lg gc", Label("status"), func() {
	It("updates status.json's horizon and counts", func() {
		s := harness.NewCLI()
		s.WriteConfig("repo: rosenhouse/lg\napi_url: " + s.Fake.URL() + "\ndisk_cap: 120KB\n")
		Expect(s.Main("sync")).To(Equal(0))
		before := s.Status()
		Expect(before["repos"]).To(HaveKeyWithValue("github.com/rosenhouse/lg", HaveKeyWithValue("runs", 1.0)))

		s.WriteConfig("repo: rosenhouse/lg\napi_url: " + s.Fake.URL() + "\ndisk_cap: 50KB\n")
		Expect(s.Main("gc")).To(Equal(0), s.Stderr.String())

		after := s.Status()
		repo := after["repos"].(map[string]any)["github.com/rosenhouse/lg"]
		Expect(repo).To(HaveKeyWithValue("runs", 0.0))
		Expect(repo).To(HaveKeyWithValue("attempts", 0.0))
		Expect(repo).To(HaveKeyWithValue("bytes_data", 0.0))
		Expect(repo).To(HaveKeyWithValue("newest_completed_run_created_at", BeNil()))
		Expect(repo).To(HaveKeyWithValue("lag_seconds", BeNil()))
		Expect(repo).To(HaveKeyWithValue("horizon", Not(Equal(before["repos"].(map[string]any)["github.com/rosenhouse/lg"].(map[string]any)["horizon"]))))
		Expect(repo).To(HaveKeyWithValue("disk_cap_bytes", 50000.0))
		Expect(repo).To(HaveKeyWithValue("default_branch", "main"))
		delete(before, "repos")
		delete(after, "repos")
		Expect(after).To(Equal(before))
	})

	It("writes no status.json for a store that never synced", func() {
		s := harness.NewCLI()
		s.WriteStatus("{}")
		Expect(os.Remove(s.StatusFile())).To(Succeed())

		Expect(s.Main("gc")).To(Equal(0), s.Stderr.String())
		Expect(s.StatusFile()).NotTo(BeAnExistingFile())
	})
})
