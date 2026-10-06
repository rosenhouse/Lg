package cli_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg where", Label("where"), func() {
	var (
		c       *harness.CLI
		attempt string
		job     string
	)

	BeforeEach(func() {
		c = harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		attempt = filepath.Join(c.Home, "data", "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "37129390741_lg-fixture_lg-fixture", "attempt-1")
		job = filepath.Join(attempt, "jobs", "111221289888_flaky")
	})

	// setHTMLURL rewrites the html_url of a JSON file under data/.
	setHTMLURL := func(path, url string) {
		GinkgoHelper()
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var fields map[string]any
		Expect(json.Unmarshal(raw, &fields)).To(Succeed())
		fields["html_url"] = url
		raw, err = json.Marshal(fields)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, raw, 0o644)).To(Succeed())
	}

	It("takes html_url from job.json, or else attempt.json", func() {
		setHTMLURL(filepath.Join(job, "job.json"), "https://ghes.example/job")
		setHTMLURL(filepath.Join(attempt, "attempt.json"), "https://ghes.example/run")

		Expect(c.Main("where", filepath.Join(job, "log.txt")+":3:x", filepath.Join(attempt, "fetch.json"))).To(Equal(0), c.Stderr.String())
		lines := strings.Split(strings.TrimSuffix(c.Stdout.String(), "\n"), "\n")
		Expect(lines).To(HaveExactElements(
			MatchRegexp(`"html_url":"https://ghes.example/job"`),
			MatchRegexp(`"html_url":"https://ghes.example/run"`),
		))
	})

	// decoded gives each line lg printed as a JSON object.
	decoded := func() []map[string]any {
		GinkgoHelper()
		var objects []map[string]any
		for _, line := range strings.Split(strings.TrimSuffix(c.Stdout.String(), "\n"), "\n") {
			var object map[string]any
			Expect(json.Unmarshal([]byte(line), &object)).To(Succeed(), line)
			objects = append(objects, object)
		}
		return objects
	}

	It("exits 1 naming each input it cannot decode, after printing the others", func() {
		missing := filepath.Join(c.Home, "data", "missing.txt") + ":1:x"
		host := filepath.Join(c.Home, "data", "github.com")

		Expect(c.Main("where", missing, filepath.Join(job, "log.txt"), host)).To(Equal(1))
		Expect(strings.Count(c.Stdout.String(), "\n")).To(Equal(1))
		Expect(c.Stderr.String()).To(SatisfyAll(
			ContainSubstring(strconv.Quote(missing)+" names no file"),
			ContainSubstring("github.com is not in a run dir"),
		))
	})

	It("quotes an input it cannot decode, and hints at rg -H when it starts with a line number", func() {
		Expect(c.Main("where", "x:1:\x1b]0;pwned\a", "12:2026-10-03T14:22:57Z foo bar")).To(Equal(1))
		Expect(c.Stderr.String()).To(SatisfyAll(
			ContainSubstring(`"x:1:\x1b]0;pwned\a" names no file`),
			Not(ContainSubstring("\x1b")),
			MatchRegexp(`"12:2026-10-03T14:22:57Z foo bar" names no file.*rg with -H`),
		))
		Expect(strings.Count(c.Stderr.String(), "-H")).To(Equal(1))
	})

	It("reads hits from stdin, skipping blank lines and rg's -- separators, and trimming CRLF", func() {
		log := filepath.Join(job, "log.txt")
		Expect(os.WriteFile(log, []byte("one\ntwo\nthree\n"), 0o644)).To(Succeed())
		c.Stdin = strings.NewReader(log + "-1-one\r\n" + log + ":2:two\r\n--\n\n  \n" + log + "\r\n")

		Expect(c.Main("where")).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(
			SatisfyAll(HaveKeyWithValue("path", log), HaveKeyWithValue("line", BeEquivalentTo(1)), HaveKeyWithValue("text", "one")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "two")),
			SatisfyAll(HaveKeyWithValue("path", log), Not(HaveKey("line")), Not(HaveKey("text"))),
		))
	})

	It("gives a line only when the file has that line and it holds the text", func() {
		log := filepath.Join(job, "log.txt")
		Expect(os.WriteFile(log, []byte("start\n10:15:00 ERROR disk full"), 0o644)).To(Succeed())

		Expect(c.Main("where", log+":10:15:00 ERROR disk full", log+":2:ERROR disk", log+":2", log+":3", log+":1:15:00")).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(
			SatisfyAll(Not(HaveKey("line")), HaveKeyWithValue("text", "10:15:00 ERROR disk full")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "ERROR disk")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), Not(HaveKey("text"))),
			SatisfyAll(Not(HaveKey("line")), HaveKeyWithValue("text", "3")),
			SatisfyAll(Not(HaveKey("line")), HaveKeyWithValue("text", "1:15:00")),
		))
	})

	It("reads a context line rg printed without a line number", func() {
		file := filepath.Join(attempt, "attempt.json")

		Expect(c.Main("where", file+`-  "head_branch": "main",`)).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("path", file),
			HaveKeyWithValue("text", `  "head_branch": "main",`),
		)))
	})

	It("reads no file outside the store", func(ctx SpecContext) {
		fifo := filepath.Join(GinkgoT().TempDir(), "fifo")
		Expect(syscall.Mkfifo(fifo, 0o600)).To(Succeed())
		DeferCleanup(func() {
			// Opening the write end frees a reader blocked opening the read end.
			if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				_ = w.Close()
			}
		})
		code := make(chan int, 1)
		go func() { code <- c.Main("where", fifo+":1:x") }()

		Eventually(ctx, code).WithTimeout(5 * time.Second).Should(Receive(Equal(1)))
		Expect(c.Stderr.String()).To(ContainSubstring(fifo + " is outside the store"))
	}, SpecTimeout(10*time.Second))

	It("keeps a hit's .. in its text, not its path", func() {
		log := filepath.Join(job, "log.txt")
		Expect(os.WriteFile(log, []byte("a\nb\nc\nd\n$ cd ../..\n"), 0o644)).To(Succeed())
		rel, err := filepath.Rel(filepath.Join(c.Home, "data"), log)
		Expect(err).NotTo(HaveOccurred())

		Expect(c.Main("where", rel+":5:$ cd ../..")).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("path", log),
			HaveKeyWithValue("line", BeEquivalentTo(5)),
			HaveKeyWithValue("text", "$ cd ../.."),
		)))
	})

	It("finds a path under runs/ in the repo dir that has it, from any working directory", func() {
		rel, err := filepath.Rel(filepath.Join(c.Home, "data", "github.com", "rosenhouse", "Lg"), filepath.Join(job, "log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(rel).NotTo(BeAnExistingFile())

		Expect(c.Main("where", rel)).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(HaveKeyWithValue("path", filepath.Join(job, "log.txt"))))
	})

	It("re-reads a run whose files change while it reads stdin", func() {
		Expect(c.Fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		Expect(c.Main("sync")).To(Equal(0))
		attempt2 := filepath.Join(filepath.Dir(attempt), "attempt-2")
		aside := filepath.Join(GinkgoT().TempDir(), "attempt-2")
		Expect(os.Rename(attempt2, aside)).To(Succeed())
		c.Stdin = io.MultiReader(
			strings.NewReader(filepath.Join(job, "log.txt")+"\n"),
			onRead(func() { Expect(os.Rename(aside, attempt2)).To(Succeed()) }),
			strings.NewReader(filepath.Join(attempt2, "jobs", "111221661475_flaky", "log.txt")+"\n"),
		)

		Expect(c.Main("where")).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(
			HaveKeyWithValue("attempt", BeEquivalentTo(1)),
			SatisfyAll(HaveKeyWithValue("attempt", BeEquivalentTo(2)), HaveKeyWithValue("job_id", BeEquivalentTo(111221661475))),
		))
	})

	It("exits 1 naming the run when the files of the unit holding the path do not parse", func() {
		Expect(os.WriteFile(filepath.Join(attempt, "jobs.json"), []byte("{"), 0o644)).To(Succeed())

		Expect(c.Main("where", filepath.Join(job, "log.txt"))).To(Equal(1))
		Expect(c.Stdout.String()).To(BeEmpty())
		Expect(c.Stderr.String()).To(SatisfyAll(
			ContainSubstring(filepath.Dir(attempt)+": cannot read its job 111221289888_flaky"),
			ContainSubstring("jobs.json"),
		))
	})

	It("exits 1 naming the run when no unit of it parses", func() {
		units, err := filepath.Glob(filepath.Join(filepath.Dir(attempt), "artifacts", "*", "artifact.json"))
		Expect(err).NotTo(HaveOccurred())
		for _, file := range append(units, filepath.Join(attempt, "attempt.json")) {
			Expect(os.WriteFile(file, []byte("{"), 0o644)).To(Succeed())
		}

		Expect(c.Main("where", filepath.Join(job, "log.txt"))).To(Equal(1))
		Expect(c.Stdout.String()).To(BeEmpty())
		Expect(c.Stderr.String()).To(ContainSubstring(filepath.Dir(attempt) + ": cannot read the run"))
	})

	It("decodes an artifact.zip.tombstone", func() {
		c = harness.NewCLI()
		c.WriteConfig("repo: rosenhouse/lg\napi_url: " + c.Fake.URL() + "\nartifact_max_bytes: 700\n")
		Expect(c.Main("sync")).To(Equal(0))
		tombstone := filepath.Join(c.Home, "data", "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "37129390741_lg-fixture_lg-fixture", "artifacts", "11276272069_pass-artifact", "artifact.zip.tombstone")

		Expect(c.Main("where", tombstone)).To(Equal(0), c.Stderr.String())
		var p map[string]any
		Expect(json.Unmarshal(c.Stdout.Bytes(), &p)).To(Succeed())
		Expect(p).To(SatisfyAll(
			HaveKeyWithValue("artifact_id", BeEquivalentTo(11276272069)),
			HaveKeyWithValue("reason", "too_large"),
			HaveKeyWithValue("http_status", BeNil()),
			HaveKeyWithValue("message", MatchRegexp(`^size_in_bytes \d+ exceeds artifact_max_bytes 700$`)),
		))
	})

	It("gives a carried-forward job's original log only while it exists", func() {
		Expect(c.Fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		Expect(c.Main("sync")).To(Equal(0))
		carried := filepath.Join(filepath.Dir(attempt), "attempt-2", "jobs", "111221662305_build-ubuntu-latest-1.23", "job.json")
		original := filepath.Join(attempt, "jobs", "111221289911_build-ubuntu-latest-1.23", "log.txt")

		Expect(c.Main("where", carried)).To(Equal(0))
		Expect(c.Stdout.String()).To(ContainSubstring(`"original_log":"` + original + `"`))
		Expect(os.Remove(original)).To(Succeed())
		Expect(c.Main("where", carried)).To(Equal(0))
		Expect(c.Stdout.String()).To(SatisfyAll(ContainSubstring(`"original_job_id":111221289911`), Not(ContainSubstring("original_log"))))
	})
})

// onRead is a reader that calls f on its first read, and is empty.
type onRead func()

func (f onRead) Read([]byte) (int, error) {
	f()
	return 0, io.EOF
}
