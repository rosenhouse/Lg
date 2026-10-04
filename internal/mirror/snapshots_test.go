package mirror_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
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

var _ = Describe("mirror.Cycle when a run's artifact listing fails transiently", Label("artifacts"), func() {
	It("publishes no attempt of that run, since it would lack its snapshot, and the next cycle publishes it", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "runs/37129390741/artifacts", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.AttemptDirs(runID)).To(BeEmpty())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(filepath.Join(attemptDir(env, runID, 1), "artifacts.json")).To(BeARegularFile())
	}, cycleTimeout)
})

func listedArtifact(id int64) github.Artifact {
	raw := fmt.Sprintf(`{"id":%d,"name":"a%d"}`, id, id)
	return github.Artifact{Artifact: model.Artifact{ID: id, Name: fmt.Sprintf("a%d", id)}, Raw: json.RawMessage(raw)}
}

func writeSnapshot(attemptDir string, artifacts ...github.Artifact) {
	GinkgoHelper()
	raws := make([]json.RawMessage, len(artifacts))
	for i, a := range artifacts {
		raws[i] = a.Raw
	}
	raw, err := json.Marshal(raws)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.MkdirAll(attemptDir, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(attemptDir, "artifacts.json"), raw, 0o644)).To(Succeed())
}

func ids(artifacts []github.Artifact) []int64 {
	var ids []int64
	for _, a := range artifacts {
		ids = append(ids, a.ID)
	}
	return ids
}

var _ = Describe("the retry set", Label("artifacts"), func() {
	It("is the ids in every attempt-*/artifacts.json, in state/pending-artifacts.json and in the current listing, minus the artifact dirs on disk", func() {
		env := harness.InProcess()
		runDir := filepath.Join(env.Data(), "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "1_w_b")
		writeSnapshot(layout.AttemptDir(runDir, 1), listedArtifact(1), listedArtifact(7))
		writeSnapshot(layout.AttemptDir(runDir, 2), listedArtifact(2), listedArtifact(3))
		for _, id := range []int64{1, 6} {
			Expect(os.MkdirAll(layout.ArtifactDir(runDir, id, fmt.Sprintf("a%d", id)), 0o755)).To(Succeed())
		}
		listing := []github.Artifact{listedArtifact(5), listedArtifact(6), listedArtifact(3)}
		pending := []github.Artifact{listedArtifact(4), listedArtifact(5)}

		retry, err := env.Mirror.RetrySet(runDir, listing, pending)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(retry)).To(Equal([]int64{5, 3, 4, 7, 2}))
		Expect(retry[3].Raw).To(MatchJSON(listedArtifact(7).Raw))
	})
})

// readPending reads state/pending-artifacts.json as run id -> artifact objects.
func readPending(env *harness.InProcessEnv) map[string][]json.RawMessage {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(env.State(), "pending-artifacts.json"))
	Expect(err).NotTo(HaveOccurred())
	var pending map[string][]json.RawMessage
	Expect(json.Unmarshal(raw, &pending)).To(Succeed())
	return pending
}

// servedArtifacts is the run's artifact listing as served, by id.
func servedArtifacts(env *harness.InProcessEnv) map[string]json.RawMessage {
	GinkgoHelper()
	var listing struct{ Artifacts []json.RawMessage }
	Expect(json.Unmarshal(env.Fake.Served("artifacts.json"), &listing)).To(Succeed())
	byID := map[string]json.RawMessage{}
	for _, raw := range listing.Artifacts {
		var a struct{ ID json.Number }
		Expect(json.Unmarshal(raw, &a)).To(Succeed())
		byID[a.ID.String()] = raw
	}
	return byID
}

var _ = Describe("state/pending-artifacts.json", Label("artifacts"), func() {
	It("keeps each listed artifact object until its dir exists", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(readPending(env)).To(HaveKeyWithValue("37129390741", ConsistOf(MatchJSON(servedArtifacts(env)[flakyReport]))))

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readPending(env)).To(BeEmpty())
	}, cycleTimeout)

	It("holds the run's listed artifacts before any zip is requested", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "30"}})

		Expect(env.Sync(ctx)).To(BeBlocked(failure.RateLimit))
		var listed []any
		for _, raw := range servedArtifacts(env) {
			listed = append(listed, MatchJSON(raw))
		}
		Expect(readPending(env)).To(HaveKeyWithValue("37129390741", ConsistOf(listed...)))
	}, cycleTimeout)
})
