package mirror_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
)

var _ = Describe("mirror.Cycle", Label("retention"), func() {
	var (
		env *harness.InProcessEnv
		old string
	)

	BeforeEach(func() {
		env = harness.InProcess()
		old = filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/2026-06-01/1_ci_main")
		Expect(os.MkdirAll(old, 0o755)).To(Succeed())
		env.FS.FailOnUnder("rename", old, syscall.EACCES)
	})

	It("returns retention's error, blocked as local_io when it cannot evict", func(ctx SpecContext) {
		Expect(env.Sync(ctx)).To(BeBlocked(failure.LocalIO))
		Expect(old).To(BeADirectory())
	}, cycleTimeout)

	It("reports a cycle whose retention fails as not completed", Label("status"), func(ctx SpecContext) {
		report, err := env.Mirror.Cycle(ctx)

		Expect(err).To(BeBlocked(failure.LocalIO))
		Expect(report.Completed).To(BeFalse())
	}, cycleTimeout)

	It("returns retention's error beside the cycle's", func(ctx SpecContext) {
		env.Fake.Fail("api", "/repos/rosenhouse/lg", fakegithub.Fault{Status: http.StatusInternalServerError})

		err := env.Sync(ctx)
		Expect(err).To(BeTransient())
		Expect(err).To(BeBlocked(failure.LocalIO))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle after its context is cancelled", Label("retention"), func() {
	It("does not evict", func(ctx SpecContext) {
		env := harness.InProcess()
		old := filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/2026-06-01/1_ci_main")
		Expect(os.MkdirAll(old, 0o755)).To(Succeed())
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		Expect(env.Sync(cancelled)).To(MatchError(context.Canceled))
		Expect(old).To(BeADirectory())
	}, cycleTimeout)

	It("leaves an empty date dir", func(ctx SpecContext) {
		env := harness.InProcess()
		empty := filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/2026-06-01")
		Expect(os.MkdirAll(empty, 0o755)).To(Succeed())
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		Expect(env.Sync(cancelled)).To(MatchError(context.Canceled))
		Expect(empty).To(BeADirectory())
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle whose ctx ends after its last request", Label("status"), func() {
	It("reports the cycle as not completed, since retention did not run", func(ctx SpecContext) {
		env := harness.InProcess()
		cancelled, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		env.FS.Before("rename", filepath.Join(env.State(), "rescan.json"), cancel)

		report, err := env.Mirror.Cycle(cancelled)

		Expect(err).To(MatchError(context.Canceled))
		Expect(report.Completed).To(BeFalse())
	}, cycleTimeout)
})
