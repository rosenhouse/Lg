package harness_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

// tampering changes a published file as the cycle starts.
type tampering struct{ path string }

func (t tampering) Token(context.Context, string) (string, error) {
	return fakegh.Token, os.WriteFile(t.path, []byte("changed"), 0o644)
}

var _ = Describe("InProcessEnv.Sync", Label("attempts"), func() {
	It("fails a cycle that changes a file under data/", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(37129390741, "after-attempt-1")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		env.Mirror.Tokens = tampering{path: filepath.Join(env.AttemptDirs(37129390741)[0], "attempt.json")}

		Expect(InterceptGomegaFailures(func() { _ = env.Sync(ctx) })).NotTo(BeEmpty())
	})
})
