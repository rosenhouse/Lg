package fakegithub_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

// recordingsCopy is a temp dir holding record.sh and the artifacts.json of
// each sibling stage, from which record.sh takes artifact ids.
func recordingsCopy(siblings ...recording) string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	copyFile(filepath.Join(recordings.Root(), "record.sh"), filepath.Join(dir, "record.sh"))
	for _, s := range siblings {
		stage := filepath.Join(dir, filepath.Base(filepath.Dir(recordings.Dir(s.run, s.stage))), s.stage)
		Expect(os.MkdirAll(stage, 0o755)).To(Succeed())
		copyFile(filepath.Join(recordings.Dir(s.run, s.stage), "artifacts.json"), filepath.Join(stage, "artifacts.json"))
	}
	return dir
}

func copyFile(from, to string) {
	GinkgoHelper()
	data, err := os.ReadFile(from)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(to, data, 0o755)).To(Succeed())
}

// record runs dir's record.sh against fake, with tmpDir as TMPDIR and
// nothing else of the caller's environment but PATH.
func record(fake *fakegithub.Server, dir, tmpDir string, run int64, label string) (output string, err error) {
	GinkgoHelper()
	cmd := exec.CommandContext(context.Background(), "bash", "record.sh", strconv.FormatInt(run, 10), label)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmpDir, "GITHUB_API_URL=" + fake.URL()}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readFile(path string) string {
	GinkgoHelper()
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

// proxyEverything configures a dead proxy, which record.sh must not inherit.
func proxyEverything() {
	GinkgoHelper()
	GinkgoT().Setenv("http_proxy", "http://127.0.0.1:9")
	GinkgoT().Setenv("no_proxy", "example.invalid")
	GinkgoT().Setenv("NO_PROXY", "example.invalid")
}

var _ = Describe("record.sh", Label("transport"), func() {
	BeforeEach(proxyEverything)

	It("re-records a stage from the fake with the recording's status.txt, dated by the run GET's Date header", func() {
		fake := fakegithub.Start(logsDeletedRun, "after-expiry")
		dir := recordingsCopy(recording{logsDeletedRun, "logs-deleted"})

		output, err := record(fake, dir, GinkgoT().TempDir(), logsDeletedRun, "after-expiry")
		Expect(err).NotTo(HaveOccurred(), output)

		stage := filepath.Join(dir, "run-37129738159", "after-expiry")
		Expect(readFile(filepath.Join(stage, "status.txt"))).To(Equal(readFile(filepath.Join(recordings.Dir(logsDeletedRun, "after-expiry"), "status.txt"))))
		Expect(readFile(filepath.Join(stage, "recorded_at.txt"))).To(Equal(recordings.DefaultNow().Format(http.TimeFormat) + "\n"))
		Expect(readFile(filepath.Join(stage, "run.json"))).To(MatchJSON(readFile(filepath.Join(recordings.Dir(logsDeletedRun, "after-expiry"), "run.json"))))
	})

	It("exits 1 naming GitHub's answer and writes no stage when the run GET fails", func() {
		fake := fakegithub.Start(runID, "after-attempt-1")
		dir := recordingsCopy()

		output, err := record(fake, dir, GinkgoT().TempDir(), 1, "bogus")
		Expect(err).To(MatchError("exit status 1"), output)
		Expect(output).To(ContainSubstring("record.sh: 404 runs/1: Not Found"))
		Expect(filepath.Join(dir, "run-1")).NotTo(BeADirectory())
	})
})
