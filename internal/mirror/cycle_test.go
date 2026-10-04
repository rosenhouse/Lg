package mirror_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

const runID = 37129390741

// staticTokens gives token, or err, for every host it is asked about.
type staticTokens struct {
	token string
	err   error
	hosts []string
}

func (s *staticTokens) Token(_ context.Context, host string) (string, error) {
	s.hosts = append(s.hosts, host)
	return s.token, s.err
}

var _ = Describe("Cycle", Label("sync"), func() {
	var (
		root      string
		recording string
		fake      *fakegithub.Server
		tokens    *staticTokens
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
		tokens = &staticTokens{token: "gho_cycle"}
		m = mirror.Mirror{
			Tokens: tokens,
			NewGitHub: func(token string) github.Client {
				return github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse/lg", token)
			},
			Store:    s,
			Host:     "github.com",
			Repo:     "rosenhouse/lg",
			Clock:    clock.NewFake(harness.DefaultNow),
			LogGrace: time.Hour,
		}
	})

	It("asks Tokens for its host's token once and sends it", Label("transport"), func() {
		fake.RequireToken("gho_cycle")
		m.Host = "ghe.corp.example"

		Expect(m.Cycle(context.Background())).To(Succeed())
		Expect(tokens.hosts).To(Equal([]string{"ghe.corp.example"}))
		Expect(os.ReadDir(filepath.Join(root, "data/ghe.corp.example"))).NotTo(BeEmpty())
	})

	It("returns the error from Tokens and sends no request", Label("transport"), func() {
		tokens.err = errors.New("gh: not logged in")

		Expect(m.Cycle(context.Background())).To(MatchError("gh: not logged in"))
		Expect(fake.Requests()).To(BeEmpty())
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

	It("writes jobs.json joining all pages into one array whose elements are JSON-equal to those served", Label("transport"), func() {
		fake.SetPageCap(5)

		Expect(m.Cycle(context.Background())).To(Succeed())
		var stored, served []json.RawMessage
		raw, err := os.ReadFile(filepath.Join(root, "data/github.com/rosenhouse/Lg/runs/2026-10-03/37129390741_lg-fixture_lg-fixture/attempt-1/jobs.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &stored)).To(Succeed())
		var listing struct{ Jobs []json.RawMessage }
		Expect(json.Unmarshal(fake.Served("attempt-1/jobs.json"), &listing)).To(Succeed())
		served = listing.Jobs
		Expect(stored).To(HaveLen(len(served)))
		for i := range served {
			Expect(stored[i]).To(MatchJSON(served[i]))
		}
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

func mustParse(rawURL string) *url.URL {
	GinkgoHelper()
	u, err := url.Parse(rawURL)
	Expect(err).NotTo(HaveOccurred())
	return u
}
