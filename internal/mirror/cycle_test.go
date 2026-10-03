package mirror_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
)

const runID = 37129390741

var _ = Describe("Cycle", Label("sync"), func() {
	var (
		root      string
		recording string
		fake      *fakegithub.Server
		m         mirror.Mirror
	)

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "lg")
		Expect(store.Init(root)).To(Succeed())
		s, err := store.Open(root)
		Expect(err).NotTo(HaveOccurred())
		recording = filepath.Join(GinkgoT().TempDir(), "recording")
		Expect(os.CopyFS(recording, os.DirFS(fakegithub.Recording(runID, "after-attempt-1")))).To(Succeed())
		fake = fakegithub.New()
		DeferCleanup(fake.Close)
		Expect(fake.LoadDir(runID, recording)).To(Succeed())
		m = mirror.Mirror{
			GitHub: github.NewHTTP(http.DefaultClient, fake.URL(), "rosenhouse/lg"),
			Store:  s,
			Host:   "github.com",
			Repo:   "rosenhouse/lg",
		}
	})

	DescribeTable("refuses a run of another repository and writes nothing",
		func(fullName string) {
			editJSON(filepath.Join(recording, "run.json"), func(run map[string]any) {
				run["repository"].(map[string]any)["full_name"] = fullName
			})

			Expect(m.Cycle(context.Background())).To(MatchError(
				`run 37129390741 belongs to "` + fullName + `", not "rosenhouse/lg"`))
			Expect(os.ReadDir(filepath.Join(root, "data"))).To(BeEmpty())
			Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty())
		},
		Entry("another repo", "other/lg"),
		Entry("a path out of the store", "../../../../escaped"),
		Entry("no name", ""),
	)

	It("requests only the run listing and leaves tmp/ empty when the attempt is already on disk", func() {
		Expect(m.Cycle(context.Background())).To(Succeed())
		before := len(fake.Requests())

		Expect(m.Cycle(context.Background())).To(Succeed())
		Expect(fake.Requests()[before:]).To(ConsistOf(
			HaveField("Path", "/repos/rosenhouse/lg/actions/runs")))
		Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty())
	})

	It("removes its staged unit when a log download fails", Label("store"), func() {
		fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusInternalServerError})

		Expect(m.Cycle(context.Background())).To(MatchError(ContainSubstring("500")))
		Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty())
		Expect(os.ReadDir(filepath.Join(root, "data"))).To(BeEmpty())
	})

	It("publishes no attempt still in progress and requests none of its jobs", func() {
		editJSON(filepath.Join(recording, "attempt-1", "attempt.json"), func(attempt map[string]any) {
			attempt["status"] = "in_progress"
			attempt["conclusion"] = nil
		})

		Expect(m.Cycle(context.Background())).To(Succeed())
		Expect(os.ReadDir(filepath.Join(root, "data"))).To(BeEmpty())
		Expect(fake.Requests()).NotTo(ContainElement(HaveField("Path", ContainSubstring("/jobs"))))
	})
})

// editJSON rewrites a recorded JSON object in place.
func editJSON(path string, edit func(map[string]any)) {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	Expect(decoder.Decode(&object)).To(Succeed())
	edit(object)
	raw, err = json.Marshal(object)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, raw, 0o644)).To(Succeed())
}
