package layout_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("Parse", Label("where"), func() {
	const (
		run      = "github.com/rosenhouse/Lg/runs/2026-10-03/37129390741_lg-fixture_lg-fixture"
		attempt  = run + "/attempt-2"
		job      = attempt + "/jobs/111221661475_flaky"
		artifact = run + "/artifacts/11276401837_flaky-report"
	)
	inRun := layout.Location{Host: "github.com", Repo: "rosenhouse/Lg", RunID: 37129390741, RunDir: run}
	inAttempt := inRun
	inAttempt.Attempt, inAttempt.AttemptDir = 2, attempt
	inJob := inAttempt
	inJob.JobID, inJob.JobDir = 111221661475, job
	inArtifact := inRun
	inArtifact.ArtifactID, inArtifact.ArtifactDir = 11276401837, artifact
	with := func(l layout.Location, file string) layout.Location {
		l.File = file
		return l
	}

	DescribeTable("decodes a path relative to data/",
		func(path string, want layout.Location) {
			Expect(layout.Parse(path)).To(Equal(want))
		},
		Entry("a run dir", run, inRun),
		Entry("an attempt dir", attempt, inAttempt),
		Entry("an attempt's file", attempt+"/fetch.json", with(inAttempt, "fetch.json")),
		Entry("a job dir", job, inJob),
		Entry("a log", job+"/log.txt", with(inJob, "log.txt")),
		Entry("a tombstone", job+"/log.txt.tombstone", with(inJob, "log.txt.tombstone")),
		Entry("an artifact", artifact+"/artifact.zip", with(inArtifact, "artifact.zip")),
		Entry("an extracted file", artifact+"/extracted/report.zip.d/a:b.log", with(inArtifact, "extracted/report.zip.d/a:b.log")),
		Entry("an unclean path", "./"+run+"//attempt-2/jobs/../fetch.json", with(inAttempt, "fetch.json")),
	)

	DescribeTable("refuses a path that names no run dir or a malformed dir in it",
		func(path string) {
			_, err := layout.Parse(path)
			Expect(err).To(MatchError(ContainSubstring(path)))
		},
		Entry("a repo dir", "github.com/rosenhouse/Lg/runs/2026-10-03"),
		Entry("no runs dir", "github.com/rosenhouse/Lg/rns/2026-10-03/1_a_b"),
		Entry("a bad date", "github.com/rosenhouse/Lg/runs/2026-13-03/1_a_b"),
		Entry("a bad repo", "github.com/rosenhouse/../runs/2026-10-03/1_a_b"),
		Entry("a bad host", "../rosenhouse/Lg/runs/2026-10-03/1_a_b"),
		Entry("an absolute path", "/github.com/rosenhouse/Lg/runs/2026-10-03/1_a_b"),
		Entry("a run dir with no id", "github.com/rosenhouse/Lg/runs/2026-10-03/x_a_b"),
		Entry("an attempt dir with no number", run+"/attempt-x/fetch.json"),
		Entry("another dir in a run", run+"/other"),
		Entry("a job dir with no id", attempt+"/jobs/x_flaky/log.txt"),
		Entry("an artifact dir with no id", run+"/artifacts/flaky-report/artifact.zip"),
	)

	It("round-trips every recorded run, job and artifact name", func() {
		repo := layout.RepoDir("", "github.com", "rosenhouse/Lg")
		attempts, err := filepath.Glob(filepath.Join(recordings.Root(), "*", "*", "attempt-*", "attempt.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(attempts).NotTo(BeEmpty())
		for _, file := range attempts {
			var r model.Run
			decode(file, &r)
			runDir := layout.RunDir(repo, r)
			Expect(layout.Parse(runDir)).To(HaveField("RunDir", runDir))

			var listing struct{ Jobs []model.Job }
			decode(filepath.Join(filepath.Dir(file), "jobs.json"), &listing)
			for _, j := range listing.Jobs {
				jobDir := layout.JobDir(layout.AttemptDir(runDir, r.RunAttempt), j.ID, j.Name)
				Expect(layout.Parse(filepath.Join(jobDir, "log.txt"))).To(SatisfyAll(
					HaveField("JobID", j.ID), HaveField("JobDir", jobDir), HaveField("File", "log.txt")), j.Name)
			}
		}

		listings, err := filepath.Glob(filepath.Join(recordings.Root(), "*", "*", "artifacts.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(listings).NotTo(BeEmpty())
		for _, file := range listings {
			var listing struct{ Artifacts []model.Artifact }
			decode(file, &listing)
			for _, a := range listing.Artifacts {
				dir := layout.ArtifactDir(filepath.FromSlash(run), a.ID, a.Name)
				Expect(layout.Parse(filepath.Join(dir, "artifact.zip"))).To(SatisfyAll(
					HaveField("ArtifactID", a.ID), HaveField("ArtifactDir", dir), HaveField("File", "artifact.zip")), a.Name)
			}
		}
	})
})

func decode(path string, v any) {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(raw, v)).To(Succeed())
}
