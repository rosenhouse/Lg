package e2e_test

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg sync --wait with a daemon mid-cycle", Label("sync"), func() {
	It("exits 0 only after a cycle that started after its request finishes, so fakegithub sees a second runs-list request first", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		release := holdFirstCycle(ctx, env, fake)

		waiting := env.Lg("sync", "--wait")
		Eventually(requested(env), cycleWait).WithContext(ctx).Should(Equal("1\n"))
		listings := len(fakegithub.RunListings(fake.Requests()))
		releaseListings := fake.Hold("/actions/runs")
		DeferCleanup(releaseListings)
		release()

		Eventually(func() int { return len(fakegithub.RunListings(fake.Requests())) }, cycleWait).WithContext(ctx).Should(BeNumerically(">", listings))
		Consistently(waiting, 2*time.Second).WithContext(ctx).ShouldNot(gexec.Exit())
		releaseListings()
		Eventually(waiting, cycleWait).WithContext(ctx).Should(gexec.Exit(0))
		Expect(cycleListings(fake)).To(Equal(2))
	}, daemonTimeout)
})

var _ = Describe("lg sync --wait with a daemon running", Label("sync"), func() {
	It("exits 3 with the blocked reason when that cycle is blocked", func(ctx SpecContext) {
		env, _ := newDaemonEnv()
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).WithContext(ctx).Should(Equal(1.0))
		env.GH().Fail("no oauth token found for github.com")

		waiting := env.Lg("sync", "--wait")

		Eventually(waiting, cycleWait).WithContext(ctx).Should(gexec.Exit(3))
		Expect(waiting.Err).To(gbytes.Say(`lg: blocked \(auth\): .*no oauth token found for github.com`))
	}, daemonTimeout)

	It("exits 1 with the errors of a cycle that completes with them", func(ctx SpecContext) {
		env, _ := newDaemonEnv()
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).WithContext(ctx).Should(Equal(1.0))
		Expect(os.WriteFile(filepath.Join(env.State(), "watch.json"), []byte("{not json"), 0o644)).To(Succeed())

		waiting := env.Lg("sync", "--wait")

		Eventually(waiting, cycleWait).WithContext(ctx).Should(gexec.Exit(1))
		Expect(waiting.Err).To(gbytes.Say(`lg: .*watch.json: moved to .*watch.json.corrupt`))
	}, daemonTimeout)

	It("exits 0 only once lg.db lists what that cycle published", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		env.Start("daemon", "run")
		Eventually(indexedAttempts(ctx, env), cycleWait).WithContext(ctx).Should(ConsistOf(1))
		Expect(fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		db, err := sql.Open("sqlite", filepath.Join(env.State(), "lg.db")+"?_pragma=busy_timeout(10000)&_txlock=immediate")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		writer, err := db.BeginTx(ctx, nil)
		Expect(err).NotTo(HaveOccurred())

		waiting := env.Lg("sync", "--wait")
		Eventually(env.Status, cycleWait).WithContext(ctx).Should(HaveKeyWithValue("served_request", 1.0))
		// A -race lg sleeps 1s as it exits.
		Consistently(waiting, 3*time.Second).WithContext(ctx).ShouldNot(gexec.Exit())
		Expect(writer.Rollback()).To(Succeed())

		Eventually(waiting, cycleWait).WithContext(ctx).Should(gexec.Exit(0))
		Expect(indexedAttempts(ctx, env)()).To(ConsistOf(1, 2))
	}, daemonTimeout)

	It("coalesces concurrent --wait calls into one cycle", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		release := holdFirstCycle(ctx, env, fake)

		first, second := env.Lg("sync", "--wait"), env.Lg("sync", "--wait")
		Eventually(requested(env), cycleWait).WithContext(ctx).Should(Equal("2\n"))
		release()

		Eventually(first, cycleWait).WithContext(ctx).Should(gexec.Exit(0))
		Eventually(second, cycleWait).WithContext(ctx).Should(gexec.Exit(0))
		Expect(cycleListings(fake)).To(Equal(2))
		Expect(env.Status()).To(And(HaveKeyWithValue("cycle", 2.0), HaveKeyWithValue("served_request", 2.0)))
	}, daemonTimeout)
})

var _ = Describe("lg sync --wait --timeout 1s", Label("sync"), func() {
	It("exits 4 when the cycle does not finish in time", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		holdFirstCycle(ctx, env, fake)

		waiting := env.Lg("sync", "--wait", "--timeout", "1s")

		Eventually(waiting, harness.ExitTimeout).WithContext(ctx).Should(gexec.Exit(4))
		Expect(waiting.Err).To(gbytes.Say(`gave up after 1s`))
	}, daemonTimeout)
})

var _ = Describe("lg sync --wait when the daemon dies", Label("sync"), func() {
	It("exits 1 at once saying the daemon exited", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		DeferCleanup(fake.Hold(heldLog))
		running := env.Start("daemon", "run")
		Eventually(fake.Requests, cycleWait).WithContext(ctx).Should(ContainElement(HaveField("Path", HaveSuffix(heldLog))))
		waiting := env.Lg("sync", "--wait")
		Eventually(requested(env), cycleWait).WithContext(ctx).Should(Equal("1\n"))

		Eventually(running.Kill(), harness.ExitTimeout).WithContext(ctx).Should(gexec.Exit())

		Eventually(waiting, 5*time.Second).WithContext(ctx).Should(gexec.Exit(1))
		Expect(waiting.Err).To(gbytes.Say("the daemon exited"))
	}, daemonTimeout)
})

var _ = Describe("lg sync --wait while rate limited past the timeout", Label("sync"), func() {
	It("exits 3 at once naming retry_at", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "3600"}})
		env.Start("daemon", "run")
		Eventually(cycle(env), cycleWait).WithContext(ctx).Should(Equal(1.0))
		retryAt := timeAt(env.Status()["blocked"].(map[string]any), "retry_at")

		waiting := env.Lg("sync", "--wait")

		Eventually(waiting, harness.ExitTimeout).WithContext(ctx).Should(gexec.Exit(3))
		Expect(waiting.Err).To(gbytes.Say(`lg: .*blocked \(rate_limit, retry_at %s\)`, retryAt.Format(time.RFC3339)))
	}, daemonTimeout)
})

var _ = Describe("lg sync with no daemon", Label("sync"), func() {
	It("runs one cycle in-process under the write lock, waiting for a concurrent one-shot to finish", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		release := fake.Hold(heldLog)
		DeferCleanup(release)
		_, firstExit := env.StartSync()
		Eventually(fake.Requests, cycleWait).WithContext(ctx).Should(ContainElement(HaveField("Path", HaveSuffix(heldLog))))

		second, secondExit := env.StartSync("--wait")
		Eventually(second.Err, cycleWait).WithContext(ctx).Should(gbytes.Say("waiting for .*write.lock"))
		Consistently(second, time.Second).WithContext(ctx).ShouldNot(gexec.Exit())
		release()

		Expect(firstExit()).To(gexec.Exit(0))
		Expect(secondExit()).To(gexec.Exit(0))
		Expect(cycle(env)()).To(Equal(2.0))
		Expect(cycleListings(fake)).To(Equal(2))
		Expect(filepath.Join(env.State(), "sync-request")).NotTo(BeAnExistingFile())
	}, daemonTimeout)
})

var _ = Describe("lg sync with no daemon while another process holds the write lock", Label("sync"), func() {
	It("exits 4 naming the holder's pid once --timeout runs out", func(ctx SpecContext) {
		env, _ := newDaemonEnv()
		holdWriteLock(env)

		session := env.Lg("sync", "--timeout", "1s")

		Eventually(session, harness.ExitTimeout).WithContext(ctx).Should(gexec.Exit(4))
		Expect(session.Err).To(gbytes.Say(`write.lock is held by pid %d; gave up after 1s`, os.Getpid()))
	}, daemonTimeout)
})

var _ = Describe("lg sync with no daemon, when a daemon starts while it waits for the write lock", Label("sync"), func() {
	It("still completes its own cycle", func(ctx SpecContext) {
		env, fake := newDaemonEnv()
		release := holdWriteLock(env)
		oneShot, oneShotExit := env.StartSync()
		Eventually(oneShot.Err, cycleWait).WithContext(ctx).Should(gbytes.Say("waiting for .*write.lock"))

		env.Start("daemon", "run")
		Eventually(held(env, "daemon.lock"), cycleWait).WithContext(ctx).Should(BeTrue())
		release()

		Expect(oneShotExit()).To(gexec.Exit(0))
		Expect(string(oneShot.Err.Contents())).NotTo(ContainSubstring("sent sync request"))
		Expect(filepath.Join(env.State(), "sync-request")).NotTo(BeAnExistingFile())
		Eventually(cycle(env), cycleWait).WithContext(ctx).Should(Equal(2.0))
		Expect(cycleListings(fake)).To(Equal(2))
	}, daemonTimeout)
})

// holdFirstCycle starts a daemon whose first cycle stalls on heldLog until
// release, which the spec's end also calls.
func holdFirstCycle(ctx SpecContext, env *harness.Env, fake *fakegithub.Server) (release func()) {
	GinkgoHelper()
	release = fake.Hold(heldLog)
	DeferCleanup(release)
	env.Start("daemon", "run")
	Eventually(fake.Requests, cycleWait).WithContext(ctx).Should(ContainElement(HaveField("Path", HaveSuffix(heldLog))))
	return release
}

// holdWriteLock takes env's state/write.lock in this process until release
// or the spec's end.
func holdWriteLock(env *harness.Env) (release func()) {
	GinkgoHelper()
	Expect(os.MkdirAll(env.State(), 0o755)).To(Succeed())
	writer, err := lock.Wait(filepath.Join(env.State(), "write.lock"), time.Second, clock.Real{}, func(string) {})
	Expect(err).NotTo(HaveOccurred())
	release = sync.OnceFunc(func() { Expect(writer.Release()).To(Succeed()) })
	DeferCleanup(release)
	return release
}

// requested gives state/sync-request, or "" before there is one.
func requested(env *harness.Env) func() string {
	return func() string {
		content, _ := os.ReadFile(filepath.Join(env.State(), "sync-request"))
		return string(content)
	}
}

// cycleListings counts the cycles that listed runs, by the one in_progress
// listing each makes.
func cycleListings(fake *fakegithub.Server) int {
	GinkgoHelper()
	n := 0
	for _, q := range fakegithub.RunListings(fake.Requests()) {
		if q.Get("status") == "in_progress" {
			n++
		}
	}
	return n
}
