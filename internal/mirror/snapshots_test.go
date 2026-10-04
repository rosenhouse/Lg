package mirror_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

func readJSONFile(path string) map[string]any {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	var fields map[string]any
	Expect(json.Unmarshal(raw, &fields)).To(Succeed())
	return fields
}

var _ = Describe("an attempt's fetch.json", Label("artifacts"), func() {
	It("records the artifact listing that artifacts.json holds", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readJSONFile(filepath.Join(attemptDir(env, runID, 1), "fetch.json"))).To(HaveKeyWithValue("sources",
			HaveKeyWithValue("artifacts.json", SatisfyAll(
				HaveKeyWithValue("url", env.Fake.URL()+"/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100"),
				HaveKeyWithValue("pages", BeEquivalentTo(1)),
			)),
		))
	}, cycleTimeout)
})
