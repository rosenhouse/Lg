package mirror_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
)

var _ = Describe("state/etags.json", Label("etags"), func() {
	var (
		env       *harness.InProcessEnv
		etags     string
		repoURL   string
		unrelated string
	)

	BeforeEach(func() {
		env = harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		etags = filepath.Join(env.State(), "etags.json")
		repoURL = env.Fake.URL() + "/repos/rosenhouse/lg"
		unrelated = repoURL + "/actions/runs/1"
	})

	read := func() map[string]github.Answer {
		GinkgoHelper()
		raw, err := os.ReadFile(etags)
		Expect(err).NotTo(HaveOccurred())
		var answers map[string]github.Answer
		Expect(json.Unmarshal(raw, &answers)).To(Succeed())
		return answers
	}

	write := func(answers map[string]github.Answer) {
		GinkgoHelper()
		raw, err := json.Marshal(answers)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(etags, raw, 0o644)).To(Succeed())
	}

	revalidated := func(urls ...string) []string {
		urls = append(urls, repoURL)
		for _, status := range mirror.NonTerminal {
			urls = append(urls, repoURL+"/actions/runs?per_page=100&status="+status)
		}
		return urls
	}

	It("holds the answers to the GETs that a cycle revalidates", func(ctx SpecContext) {
		Expect(env.Sync(ctx)).To(Succeed())

		run := fmt.Sprintf("%s/actions/runs/%d", repoURL, runID)
		Expect(slices.Collect(maps.Keys(read()))).To(ConsistOf(revalidated(run, run+"/artifacts?per_page=100")))
		Expect(read()).To(HaveEach(HaveField("ETag", Not(BeEmpty()))))
	}, cycleTimeout)

	It("keeps only the answers that a cycle that ran to its end asked for", func(ctx SpecContext) {
		Expect(env.Sync(ctx)).To(Succeed())
		answers := read()
		answers[unrelated] = github.Answer{ETag: `"stale"`, Body: []byte(`{}`)}
		write(answers)

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(slices.Collect(maps.Keys(read()))).To(ConsistOf(revalidated()))
	}, cycleTimeout)

	It("keeps every answer after a cycle that stopped early", func(ctx SpecContext) {
		write(map[string]github.Answer{unrelated: {ETag: `"stale"`, Body: []byte(`{}`)}})
		env.Fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusUnauthorized})

		Expect(env.Sync(ctx)).To(BeBlocked(failure.Auth))
		Expect(read()).To(SatisfyAll(HaveLen(2), HaveKey(unrelated), HaveKey(repoURL)))
	}, cycleTimeout)

	It("is moved aside when it does not parse, and the cycle syncs as if it were empty and reports it", func(ctx SpecContext) {
		Expect(os.WriteFile(etags, []byte("["), 0o644)).To(Succeed())

		err := env.Sync(ctx)
		Expect(err).To(MatchError(ContainSubstring(etags)))
		Expect(mirror.RunScoped(err)).To(BeTrue())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(os.ReadFile(etags + ".corrupt")).To(Equal([]byte("[")))
		Expect(read()).To(HaveKey(repoURL))
	}, cycleTimeout)
})
