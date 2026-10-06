package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
)

const waitTimeout = time.Minute

var _ = Describe("WaitForCycle", Label("sync"), func() {
	var (
		state string
		clk   *clock.Fake
	)

	BeforeEach(func() {
		state = GinkgoT().TempDir()
		clk = clock.NewFake(t0)
	})

	// runDaemon holds state/daemon.lock until the spec ends.
	runDaemon := func() {
		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
	}

	writeStatus := func(format string, args ...any) {
		Expect(os.WriteFile(filepath.Join(state, "status.json"), fmt.Appendf(nil, format, args...), 0o644)).To(Succeed())
	}

	// served is the status after a good cycle that served request n.
	served := func(n int64) {
		writeStatus(`{"last_sync_finished_at": "2026-10-03T18:00:00Z", "last_sync_ok_at": "2026-10-03T18:00:00Z", "served_request": %d}`, n)
	}

	wait := func(request int64) <-chan error {
		done := make(chan error, 1)
		go func() { done <- daemon.WaitForCycle(state, request, waitTimeout, clk) }()
		return done
	}

	// result waits a second for WaitForCycle to return, so a regression fails instead of hanging.
	result := func(request int64) error {
		GinkgoHelper()
		var err error
		Eventually(wait(request), time.Second).Should(Receive(&err))
		return err
	}

	// polling waits until WaitForCycle waits on clk for its next poll and its deadline.
	polling := func() {
		Eventually(clk.Waiting, time.Second).Should(Equal(2))
	}

	It("returns once status.json shows a finished cycle whose served_request is at least its request", func() {
		runDaemon()
		served(2)
		done := wait(3)
		polling()
		Expect(done).NotTo(Receive())

		served(4)
		clk.Set(t0.Add(time.Second))

		Eventually(done, time.Second).Should(Receive(BeNil()))
	})

	It("gives up after its timeout", func() {
		runDaemon()
		served(2)
		done := wait(3)
		polling()
		clk.Set(t0.Add(waitTimeout - time.Nanosecond))
		polling()
		Expect(done).NotTo(Receive())

		clk.Set(t0.Add(waitTimeout))

		var err error
		Eventually(done, time.Second).Should(Receive(&err))
		Expect(err).To(MatchError(lock.ErrTimeout))
		Expect(err).To(MatchError("no cycle served sync request 3; gave up after 1m0s"))
	})

	It("fails at once when no daemon holds state/daemon.lock", func() {
		served(2)

		Expect(result(3)).To(MatchError("the daemon exited before it served sync request 3"))
	})

	It("gives the served cycle's result after the daemon exited", func() {
		served(3)

		Expect(result(3)).To(Succeed())
	})

	It("waits while status.json does not parse, as the daemon will rewrite it", func() {
		runDaemon()
		writeStatus("{garbage")
		done := wait(3)
		polling()
		Expect(done).NotTo(Receive())

		served(3)
		clk.Set(t0.Add(time.Second))

		Eventually(done, time.Second).Should(Receive(BeNil()))
	})

	It("gives the parse error of status.json once the daemon has exited", func() {
		writeStatus("{garbage")

		Expect(result(3)).To(MatchError(ContainSubstring("status.json: invalid character 'g'")))
	})

	It("fails at once with the blocked reason when blocked.retry_at is past its deadline", func() {
		runDaemon()
		writeStatus(`{"served_request": 2, "blocked": {"kind": "rate_limit", "detail": "429", "retry_at": "2026-10-03T18:01:01Z"}}`)

		err := result(3)

		Expect(err).To(MatchError(failure.Blocked{Kind: failure.RateLimit, Detail: "429", RetryAt: t0.Add(waitTimeout + time.Second)}))
	})

	It("waits for a cycle when blocked.retry_at is at its deadline", func() {
		runDaemon()
		writeStatus(`{"served_request": 2, "blocked": {"kind": "rate_limit", "detail": "429", "retry_at": "2026-10-03T18:01:00Z"}}`)

		done := wait(3)
		polling()

		Expect(done).NotTo(Receive())
	})

	DescribeTable("gives the result of the cycle that served it",
		func(content string, want types.GomegaMatcher) {
			runDaemon()
			writeStatus("%s", content)

			Expect(result(3)).To(want)
		},
		Entry("blocked",
			`{"served_request": 3, "last_sync_started_at": "2026-10-03T18:00:00Z", "blocked": {"kind": "auth", "detail": "401"}}`,
			MatchError(failure.Blocked{Kind: failure.Auth, Detail: "401"})),
		Entry("failed in the second its last good sync finished",
			`{"served_request": 3, "last_sync_finished_at": "2026-10-03T18:00:00Z", "last_sync_ok_at": "2026-10-03T18:00:00Z", "last_sync_errors": ["GET /actions/runs: 500"]}`,
			MatchError("GET /actions/runs: 500")),
		Entry("completed with errors",
			`{"served_request": 3, "last_sync_finished_at": "2026-10-03T18:00:00Z", "last_sync_ok_at": "2026-10-03T18:00:00Z",
			  "last_sync_errors": ["watch.json: moved to watch.json.corrupt", "run 1 attempt 2: 502"]}`,
			MatchError("watch.json: moved to watch.json.corrupt\nrun 1 attempt 2: 502")),
		Entry("ok, serving a later request too", `{"served_request": 4, "last_sync_finished_at": "2026-10-03T18:00:00Z", "last_sync_ok_at": "2026-10-03T18:00:00Z"}`, Succeed()),
	)
})
