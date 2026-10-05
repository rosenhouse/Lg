package mirror_test

import (
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
		env.FS.FailOnUnder("rename", filepath.Join(env.Tmp(), "trash"), syscall.EACCES)
	})

	It("returns retention's error, blocked as local_io when it cannot evict", func(ctx SpecContext) {
		Expect(env.Sync(ctx)).To(BeBlocked(failure.LocalIO))
		Expect(old).To(BeADirectory())
	}, cycleTimeout)

	It("returns retention's error beside the cycle's", func(ctx SpecContext) {
		env.Fake.Fail("api", "/repos/rosenhouse/lg", fakegithub.Fault{Status: http.StatusInternalServerError})

		err := env.Sync(ctx)
		Expect(err).To(BeTransient())
		Expect(err).To(BeBlocked(failure.LocalIO))
	}, cycleTimeout)
})
