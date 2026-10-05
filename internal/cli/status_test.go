package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/execx"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/version"
)

// syncEnv runs cli.Main in-process against a fakegithub serving the fixture run.
type syncEnv struct {
	home, config   string
	fake           *fakegithub.Server
	fs             *faultfs.FS
	runner         execx.Runner
	stdout, stderr bytes.Buffer
}

func newSyncEnv() *syncEnv {
	s := &syncEnv{home: GinkgoT().TempDir(), fake: fakegithub.Start(37129390741, "after-attempt-1"), fs: faultfs.New(), runner: tokenRunner{}}
	s.config = filepath.Join(GinkgoT().TempDir(), "config.yaml")
	s.writeConfig("repo: rosenhouse/lg\napi_url: " + s.fake.URL() + "\n")
	return s
}

func (s *syncEnv) writeConfig(content string) {
	GinkgoHelper()
	Expect(os.WriteFile(s.config, []byte(content), 0o644)).To(Succeed())
}

func (s *syncEnv) main(args ...string) int {
	s.stdout.Reset()
	s.stderr.Reset()
	return cli.Main(args, cli.Deps{
		Env:    map[string]string{"LG_HOME": s.home, "LG_CONFIG": s.config, "LG_GH": "gh", "LG_TEST_NOW": harness.DefaultNow().Format(time.RFC3339)},
		Stdout: &s.stdout,
		Stderr: &s.stderr,
		Clock:  clock.Real{},
		Runner: s.runner,
		NewGitHub: func(api *url.URL, repo, token string, clk clock.Clock) github.Client {
			return github.NewHTTP(github.NewTransport(harness.ShortTimeouts()), api, repo, token, clk)
		},
		StoreFS: s.fs,
	})
}

func (s *syncEnv) statusFile() string { return filepath.Join(s.home, "state", "status.json") }

func (s *syncEnv) status() map[string]any {
	GinkgoHelper()
	raw, err := os.ReadFile(s.statusFile())
	Expect(err).NotTo(HaveOccurred())
	var st map[string]any
	Expect(json.Unmarshal(raw, &st)).To(Succeed())
	return st
}

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
	func(setup func(*syncEnv), kind string, retryAfter time.Duration) {
		s := newSyncEnv()
		setup(s)

		Expect(s.main("sync")).To(Equal(3))

		blocked := s.status()["blocked"]
		Expect(blocked).To(HaveKeyWithValue("kind", kind))
		Expect(blocked).To(HaveKeyWithValue("since", near(harness.DefaultNow())))
		Expect(blocked).To(HaveKeyWithValue("detail", Not(BeEmpty())))
		if retryAfter == 0 {
			Expect(blocked).To(HaveKeyWithValue("retry_at", BeNil()))
		} else {
			Expect(blocked).To(HaveKeyWithValue("retry_at", near(harness.DefaultNow().Add(retryAfter))))
		}
	},
	Entry("auth when gh fails", func(s *syncEnv) { s.runner = failingRunner{"not logged in"} }, "auth", time.Duration(0)),
	Entry("auth on 401", func(s *syncEnv) { s.fake.RequireToken("another-token") }, "auth", time.Duration(0)),
	Entry("rate_limit with retry_at", func(s *syncEnv) {
		s.fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "120"}})
	}, "rate_limit", 2*time.Minute),
	Entry("unreachable", func(s *syncEnv) { s.fake.Close() }, "unreachable", time.Duration(0)),
	Entry("local_io", func(s *syncEnv) { s.fs.FailOnUnder("write", filepath.Join(s.home, "tmp"), syscall.ENOSPC) }, "local_io", time.Duration(0)),
)

var _ = DescribeTable("cli.Main prints one warning line on stderr while blocked, leaving stdout unchanged", Label("status"),
	func(args []string, wantStdout func(s *syncEnv) string) {
		s := newSyncEnv()
		Expect(s.main("sync")).To(Equal(0))
		st := s.status()
		st["blocked"] = map[string]any{"since": harness.DefaultNow().Format(time.RFC3339), "kind": "auth", "detail": "HTTP 401: Bad credentials\n(see gh auth status)", "retry_at": nil}
		raw, err := json.Marshal(st)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(s.statusFile(), raw, 0o644)).To(Succeed())

		Expect(s.main(args...)).To(Equal(0))

		Expect(s.stdout.String()).To(Equal(wantStdout(s)))
		Expect(strings.Count(s.stderr.String(), "\n")).To(Equal(1), s.stderr.String())
		Expect(s.stderr.String()).To(MatchRegexp(`^lg: warning: .*blocked.*auth.*HTTP 401: Bad credentials`))
	},
	Entry("paths", []string{"paths"}, func(s *syncEnv) string {
		logs, err := filepath.Glob(filepath.Join(s.home, "data/*/*/*/runs/*/*/attempt-*/jobs/*/log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(10))
		return strings.Join(logs, "\n") + "\n"
	}),
	Entry("status", []string{"status", "--json"}, func(s *syncEnv) string {
		raw, err := os.ReadFile(s.statusFile())
		Expect(err).NotTo(HaveOccurred())
		return string(raw)
	}),
	Entry("version", []string{"version"}, func(*syncEnv) string { return version.Version + "\n" }),
)

var _ = DescribeTable("cli.Main exit codes", Label("status"),
	func(code int, setups ...func(*syncEnv) []string) {
		for _, setup := range setups {
			s := newSyncEnv()
			Expect(s.main(setup(s)...)).To(Equal(code), s.stderr.String())
		}
	},
	Entry("0 on success", 0, func(*syncEnv) []string { return []string{"sync"} }),
	Entry("1 with pending units", 1, func(s *syncEnv) []string {
		s.fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusBadGateway})
		return []string{"sync"}
	}),
	Entry("2 on usage or config errors", 2,
		func(*syncEnv) []string { return []string{"sync", "--no-such-flag"} },
		func(s *syncEnv) []string {
			s.writeConfig("repo: rosenhouse/lg\nsync_interval: 1s\n")
			return []string{"sync"}
		}),
	Entry("3 when blocked", 3, func(s *syncEnv) []string {
		s.runner = failingRunner{"not logged in"}
		return []string{"sync"}
	}),
)

var _ = Describe("lg status before any sync", Label("status"), func() {
	It("prints that it never synced, and with --json exits 1 naming state/status.json", func() {
		s := newSyncEnv()

		Expect(s.main("status")).To(Equal(0))
		Expect(s.stdout.String()).To(Equal("last sync: never\ndaemon: not running\n"))

		Expect(s.main("status", "--json")).To(Equal(1))
		Expect(s.stdout.String()).To(BeEmpty())
		Expect(s.stderr.String()).To(Equal("lg: warning: never synced; run `lg sync`\nlg: " + s.statusFile() + " does not exist\n"))
	})
})

var _ = Describe("lg status after a good sync", Label("status"), func() {
	It("prints no blocked state, next sync, horizon or daemon", func() {
		s := newSyncEnv()
		Expect(s.main("sync")).To(Equal(0))
		Expect(os.WriteFile(s.statusFile(), []byte(`{
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
`), 0o644)).To(Succeed())

		Expect(s.main("status")).To(Equal(0))
		Expect(s.stdout.String()).To(Equal(`last sync: 2026-10-03T17:59:00Z, finished 2026-10-03T17:59:30Z (cycle 2)
last ok sync: 2026-10-03T17:59:30Z
next sync: none scheduled
blocked: no
daemon: not running
github.com/rosenhouse/lg:
  default branch: main
  newest completed run: none, lag: none
  runs: 0, attempts: 0, bytes: 0
  pending units: 0
  horizon: none
  retention: 30 days, disk_cap: 1000000 bytes
`))
	})
})

var _ = DescribeTable("cli.Main on a store that never synced", Label("status"),
	func(command, warning string) {
		s := newSyncEnv()

		Expect(s.main(command)).To(Equal(0))
		Expect(s.stderr.String()).To(Equal(warning))
	},
	Entry("tells to run lg sync", "paths", "lg: warning: never synced; run `lg sync`\n"),
	Entry("does not tell lg sync to run itself", "sync", "lg: warning: never synced\n"),
)

var _ = Describe("lg sync with a pending unit", Label("status"), func() {
	It("records the unit with its error in status.json, and the sync as ok", func() {
		s := newSyncEnv()
		s.fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusBadGateway})

		Expect(s.main("sync")).To(Equal(1))

		st := s.status()
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
		s := newSyncEnv()
		Expect(s.main("sync")).To(Equal(0))
		Expect(os.WriteFile(filepath.Join(s.home, "state", name), []byte("{"), 0o644)).To(Succeed())

		Expect(s.main("sync")).To(Equal(1))
		Expect(s.stderr.String()).To(ContainSubstring(name + ".corrupt"))

		repo := s.status()["repos"].(map[string]any)["github.com/rosenhouse/lg"]
		Expect(repo).To(HaveKeyWithValue("pending_units", 0.0))
		Expect(repo).To(HaveKeyWithValue("pending", BeEmpty()))
	},
	Entry("watch.json", "watch.json"),
	Entry("pending-artifacts.json", "pending-artifacts.json"),
	Entry("rescan.json", "rescan.json"),
)

var _ = Describe("lg sync with a watched run GitHub fails to serve", Label("status"), func() {
	It("records the run as pending", func() {
		s := newSyncEnv()
		Expect(s.main("sync")).To(Equal(0))
		Expect(os.WriteFile(filepath.Join(s.home, "state", "watch.json"), []byte(`{"github.com": {"42": {"created_at": "2026-10-03T17:00:00Z"}}}`), 0o644)).To(Succeed())
		s.fake.Fail("api", "/actions/runs/42", fakegithub.Fault{Status: http.StatusBadGateway})

		Expect(s.main("sync")).To(Equal(1))

		repo := s.status()["repos"].(map[string]any)["github.com/rosenhouse/lg"]
		Expect(repo).To(HaveKeyWithValue("pending", ConsistOf(And(
			HaveKeyWithValue("run", 42.0),
			Not(HaveKey("attempt")),
			HaveKeyWithValue("error", ContainSubstring("502 Bad Gateway"))))))
		Expect(s.stderr.String()).To(ContainSubstring("run 42: "))
	})
})

var _ = Describe("cli.Main with a status.json it cannot parse", Label("status"), func() {
	It("warns naming the file and runs the command", func() {
		s := newSyncEnv()
		Expect(s.main("sync")).To(Equal(0))
		Expect(os.WriteFile(s.statusFile(), []byte("{"), 0o644)).To(Succeed())

		Expect(s.main("version")).To(Equal(0))
		Expect(s.stdout.String()).To(Equal(version.Version + "\n"))
		Expect(s.stderr.String()).To(MatchRegexp(`^lg: warning: ` + regexp.QuoteMeta(s.statusFile()) + `: [^\n]+\n$`))
	})

	It("replaces it after a good sync, which exits 0", func() {
		s := newSyncEnv()
		Expect(s.main("sync")).To(Equal(0))
		Expect(os.WriteFile(s.statusFile(), []byte(`{"cycle": 5,`), 0o644)).To(Succeed())

		Expect(s.main("sync")).To(Equal(0), s.stderr.String())
		Expect(s.status()).To(HaveKeyWithValue("cycle", 1.0))
	})
})

var _ = DescribeTable("lg sync that GitHub stops before the cycle ends", Label("status"),
	func(match string) {
		s := newSyncEnv()
		s.fake.Fail("api", match, fakegithub.Fault{Status: http.StatusBadGateway})

		Expect(s.main("sync")).To(Equal(1))

		st := s.status()
		Expect(st).To(HaveKeyWithValue("last_sync_ok_at", BeNil()))
		Expect(st["repos"]).To(HaveKeyWithValue("github.com/rosenhouse/lg", HaveKeyWithValue("pending", BeEmpty())))
		Expect(s.main("paths")).To(Equal(0))
		Expect(s.stderr.String()).To(Equal("lg: warning: no sync has succeeded yet\n"))
	},
	Entry("on GET /repos", "/repos/rosenhouse/lg"),
	Entry("on the runs listing", "/actions/runs"),
)

var _ = Describe("lg sync that cannot write status.json", Label("status"), func() {
	It("exits 3, blocked as local_io, naming status.json", func() {
		s := newSyncEnv()
		s.fs.FailOnUnder("rename", s.statusFile(), syscall.ENOSPC)

		Expect(s.main("sync")).To(Equal(3))

		Expect(s.stderr.String()).To(ContainSubstring("blocked (local_io)"))
		Expect(s.stderr.String()).To(ContainSubstring("status.json"))
		Expect(s.statusFile()).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("lg sync on a disk too full for status.json.tmp", Label("status"), func() {
	It("still records blocked in status.json, leaving no temp file", func() {
		s := newSyncEnv()
		Expect(s.main("sync")).To(Equal(0))
		s.fs.FailOnUnder("create", s.statusFile()+".tmp", syscall.ENOSPC)
		s.runner = failingRunner{"not logged in"}

		Expect(s.main("sync")).To(Equal(3))

		Expect(s.status()).To(HaveKeyWithValue("blocked", HaveKeyWithValue("kind", "auth")))
		Expect(s.statusFile() + ".tmp").NotTo(BeAnExistingFile())
	})
})
