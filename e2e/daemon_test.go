package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
)

const (
	// cycleWait bounds how long a spec waits for a daemon's cycle.
	cycleWait     = 30 * time.Second
	daemonTimeout = SpecTimeout(2 * time.Minute)
	// heldLog is a job log the fake can hold to keep a cycle running.
	heldLog = "jobs/111221289888/logs"
)

var _ = Describe("lg daemon run", Label("daemon"), func() {
	var (
		env  *harness.Env
		fake *fakegithub.Server
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
	})

	It("syncs at start, and after the fake advances to after-attempt-2 and `lg sync` writes a request, publishes attempt-2", func(SpecContext) {
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		Expect(attemptDir(env, 1)).To(BeADirectory())

		Expect(fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		Eventually(env.Lg("sync"), harness.ExitTimeout).Should(gexec.Exit(0))
		Eventually(attemptDir, cycleWait).WithArguments(env, 2).Should(BeADirectory())
	}, daemonTimeout)

	It("sets next_sync_at 10m after last_sync_started_at when sync_interval is unset", func(SpecContext) {
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))

		st := env.Status()
		Expect(timeAt(st, "next_sync_at")).To(Equal(timeAt(st, "last_sync_started_at").Add(10 * time.Minute)))
	}, daemonTimeout)

	It("starts after the previous daemon was killed with SIGKILL", func(SpecContext) {
		first := env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		Eventually(first.Kill(), harness.ExitTimeout).Should(gexec.Exit())

		second := env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(2.0))
		Expect(env.Status()).To(HaveKeyWithValue("daemon_pid", float64(second.Command.Process.Pid)))
		Expect(second).NotTo(gexec.Exit())
	}, daemonTimeout)

	It("sweeps tmp/ on start while holding the write lock", func(SpecContext) {
		Expect(store.Init(env.Store())).To(Succeed())
		dead := filepath.Join(env.Tmp(), "dead-writer")
		Expect(os.MkdirAll(dead, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dead, "log.txt"), []byte("partial"), 0o644)).To(Succeed())
		writer, err := lock.Wait(filepath.Join(env.State(), "write.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())

		env.Start("daemon", "run")
		Eventually(held(env, "daemon.lock"), cycleWait).Should(BeTrue())
		Consistently(dead, 2*time.Second).Should(BeADirectory())

		Expect(writer.Release()).To(Succeed())
		Eventually(env.Tmp(), cycleWait).Should(matchers.BeSwept())
	}, daemonTimeout)

	It("exits 0 on SIGTERM mid-cycle, releases its locks and leaves no partial unit", func(SpecContext) {
		DeferCleanup(fake.Hold(heldLog))
		Expect(os.MkdirAll(env.State(), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(env.State(), "sync-request"), []byte("7\n"), 0o644)).To(Succeed())
		daemon := env.Start("daemon", "run")
		Eventually(fake.Requests, cycleWait).Should(ContainElement(HaveField("Path", HaveSuffix(heldLog))))

		daemon.Signal(syscall.SIGTERM)

		Eventually(daemon, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(env.Status()).To(HaveKeyWithValue("served_request", 0.0))
		Expect(filepath.Join(env.State(), "daemon.pid")).NotTo(BeAnExistingFile())
		Expect(os.ReadFile(filepath.Join(env.State(), "daemon.lock"))).To(BeEmpty())
		Expect(os.ReadFile(filepath.Join(env.State(), "write.lock"))).To(BeEmpty())
		Expect(env.Tmp()).To(matchers.BeSwept())
		Expect(attemptDir(env, 1)).NotTo(BeADirectory())
	}, daemonTimeout)

	It("exits 0 on SIGTERM while another process holds write.lock, and records no cycle", func(SpecContext) {
		Expect(os.MkdirAll(env.State(), 0o755)).To(Succeed())
		writer, err := lock.Wait(filepath.Join(env.State(), "write.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(writer.Release)
		daemon := env.Start("daemon", "run")
		Eventually(daemon.Err, cycleWait).Should(gbytes.Say("waiting for .*write.lock"))

		daemon.Signal(syscall.SIGTERM)

		Eventually(daemon, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(filepath.Join(env.State(), "status.json")).NotTo(BeAnExistingFile())
	}, daemonTimeout)

	It("asks gh for a token every cycle, so a rotated token is used next time", func(SpecContext) {
		env.GH().SetToken("gho_first")
		fake.RequireToken("gho_first")
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		Expect(env.Status()).To(HaveKeyWithValue("blocked", BeNil()))

		env.GH().SetToken("gho_rotated")
		fake.RequireToken("gho_rotated")
		Eventually(env.Lg("sync"), harness.ExitTimeout).Should(gexec.Exit(0))

		Eventually(cycle(env), cycleWait).Should(Equal(2.0))
		Expect(env.Status()).To(HaveKeyWithValue("blocked", BeNil()))
		Expect(env.GH().Calls()).To(HaveLen(2))
	}, daemonTimeout)

	It("reconciles state/lg.db after each cycle, so lg.db lists attempt-2 before any reader runs", func(ctx SpecContext) {
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		Expect(fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		Eventually(env.Lg("sync"), harness.ExitTimeout).Should(gexec.Exit(0))

		Eventually(indexedAttempts(ctx, env), cycleWait).Should(ConsistOf(1, 2))
	}, daemonTimeout)
})

var _ = Describe("lg daemon run after config.yaml changes sync_interval from 1h to 2m", Label("daemon"), func() {
	It("sets next_sync_at 2m after the next cycle starts", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL(), "sync_interval: 1h")
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		Expect(untilNext(env.Status())).To(Equal(time.Hour))

		env.WriteConfig(fake.URL(), "sync_interval: 2m")
		Eventually(env.Lg("sync"), harness.ExitTimeout).Should(gexec.Exit(0))

		Eventually(cycle(env), cycleWait).Should(Equal(2.0))
		Expect(untilNext(env.Status())).To(Equal(2 * time.Minute))
	}, daemonTimeout)
})

var _ = Describe("lg daemon run after config.yaml changes while it waits for write.lock", Label("daemon"), func() {
	It("syncs with the changed config", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL(), "sync_interval: 1h")
		Expect(os.MkdirAll(env.State(), 0o755)).To(Succeed())
		writer, err := lock.Wait(filepath.Join(env.State(), "write.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		daemon := env.Start("daemon", "run")
		Eventually(daemon.Err, cycleWait).Should(gbytes.Say("waiting for .*write.lock"))

		env.WriteConfig(fake.URL(), "sync_interval: 2m")
		Expect(writer.Release()).To(Succeed())

		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		Expect(untilNext(env.Status())).To(Equal(2 * time.Minute))
	}, daemonTimeout)
})

var _ = Describe("lg daemon run after config.yaml becomes invalid", Label("daemon"), func() {
	It("keeps the last good config and records the error in status.json", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL(), "sync_interval: 1h")
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		Expect(env.Status()).To(HaveKeyWithValue("config_error", BeNil()))

		env.WriteConfig(fake.URL(), "sync_interval: 2m", "colour: blue")
		// lg sync refuses an invalid config.yaml, so the spec requests the cycle itself.
		Expect(daemon.Request(env.State(), clock.Real{})).To(Equal(int64(1)))

		Eventually(cycle(env), cycleWait).Should(Equal(2.0))
		Expect(untilNext(env.Status())).To(Equal(time.Hour))
		Expect(env.Status()).To(HaveKeyWithValue("config_error", ContainSubstring("colour")))
	}, daemonTimeout)
})

var _ = Describe("a second lg daemon run on the same store", Label("daemon"), func() {
	It("exits 1 printing `already running (pid N)`", func(SpecContext) {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		first := env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))

		second := env.Lg("daemon", "run")

		Eventually(second, harness.ExitTimeout).Should(gexec.Exit(1))
		Expect(second.Err).To(gbytes.Say(`already running \(pid %d\)`, first.Command.Process.Pid))
		Expect(first).NotTo(gexec.Exit())
	}, daemonTimeout)
})

var _ = Describe("lg daemon run when rate limited", Label("daemon"), func() {
	It("sets next_sync_at to retry_at", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "1800"}})

		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))

		st := env.Status()
		Expect(st["blocked"]).To(HaveKeyWithValue("kind", "rate_limit"))
		retryAt := timeAt(st["blocked"].(map[string]any), "retry_at")
		Expect(retryAt).To(And(
			BeTemporally(">=", timeAt(st, "last_sync_started_at").Add(30*time.Minute)),
			BeTemporally("<=", timeAt(st, "last_sync_finished_at").Add(30*time.Minute+time.Second))))
		Expect(timeAt(st, "next_sync_at")).To(Equal(retryAt))
	}, daemonTimeout)
})

var _ = Describe("lg daemon run restarted while rate limited", Label("daemon"), func() {
	It("waits for the recorded retry_at before its first cycle", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "3600"}})
		first := env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		first.Signal(syscall.SIGTERM)
		Eventually(first, harness.ExitTimeout).Should(gexec.Exit(0))

		env.Start("daemon", "run")

		Eventually(held(env, "daemon.lock"), cycleWait).Should(BeTrue())
		Consistently(cycle(env), 3*time.Second).Should(Equal(1.0))
	}, daemonTimeout)
})

var _ = Describe("lg sync without --wait, with a daemon running", Label("daemon"), func() {
	It("writes a sync request and exits 0 at once", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		DeferCleanup(fake.Hold(heldLog))
		env.Start("daemon", "run")
		Eventually(fake.Requests, cycleWait).Should(ContainElement(HaveField("Path", HaveSuffix(heldLog))))

		session := env.Lg("sync")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(os.ReadFile(filepath.Join(env.State(), "sync-request"))).To(Equal([]byte("1\n")))
	}, daemonTimeout)
})

var _ = Describe("lg sync with a daemon running and an invalid config.yaml", Label("daemon"), func() {
	It("exits 2 naming the error, as without a daemon, and sends no request", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		env.WriteConfig(fake.URL(), "sync_interval: 30s")

		session := env.Lg("sync")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say("sync_interval must be at least 1m"))
		Expect(filepath.Join(env.State(), "sync-request")).NotTo(BeAnExistingFile())
	}, daemonTimeout)
})

var _ = Describe("lg sync with a daemon blocked until a future retry_at", Label("daemon"), func() {
	It("is served by the cycle at retry_at, not at once", func(SpecContext) {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "8"}, Times: 1})
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).Should(Equal(1.0))
		retryAt := timeAt(env.Status()["blocked"].(map[string]any), "retry_at")

		Eventually(env.Lg("sync"), harness.ExitTimeout).Should(gexec.Exit(0))

		Consistently(cycle(env), 3*time.Second).Should(Equal(1.0))
		Eventually(cycle(env), cycleWait).Should(Equal(2.0))
		st := env.Status()
		Expect(timeAt(st, "last_sync_started_at")).To(BeTemporally(">=", retryAt.Truncate(time.Second)))
		Expect(st).To(HaveKeyWithValue("served_request", 1.0))
		Expect(st).To(HaveKeyWithValue("blocked", BeNil()))
	}, daemonTimeout)
})

// cycle gives the cycle number in env's status.json, or 0 before there is one.
func cycle(env *harness.Env) func() float64 {
	return func() float64 {
		raw, err := os.ReadFile(filepath.Join(env.State(), "status.json"))
		if err != nil {
			return 0
		}
		var st struct{ Cycle float64 }
		if json.Unmarshal(raw, &st) != nil {
			return 0
		}
		return st.Cycle
	}
}

// held reports whether a process holds the lock state/<name>.
func held(env *harness.Env, name string) func() bool {
	return func() bool {
		GinkgoHelper()
		held, err := lock.Held(filepath.Join(env.State(), name))
		Expect(err).NotTo(HaveOccurred())
		return held
	}
}

func attemptDir(env *harness.Env, n int) string {
	return filepath.Join(env.Data(), fixtureRunDir, fmt.Sprintf("attempt-%d", n))
}

// untilNext is next_sync_at less last_sync_started_at.
func untilNext(st map[string]any) time.Duration {
	GinkgoHelper()
	return timeAt(st, "next_sync_at").Sub(timeAt(st, "last_sync_started_at"))
}

// indexedAttempts gives the attempts in state/lg.db, or none before it exists.
func indexedAttempts(ctx context.Context, env *harness.Env) func() []int {
	return func() []int {
		GinkgoHelper()
		path := filepath.Join(env.State(), "lg.db")
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		db, err := sql.Open("sqlite", path)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = db.Close() }()
		rows, err := db.QueryContext(ctx, "SELECT attempt FROM attempts")
		if err != nil {
			return nil
		}
		defer func() { _ = rows.Close() }()
		var attempts []int
		for rows.Next() {
			var n int
			Expect(rows.Scan(&n)).To(Succeed())
			attempts = append(attempts, n)
		}
		return attempts
	}
}
