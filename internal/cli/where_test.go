package cli_test

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
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

		Expect(c.Main("where", filepath.Join(job, "log.txt"), filepath.Join(attempt, "fetch.json"))).To(Equal(0), c.Stderr.String())
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

	It("exits 1 naming each input it cannot decode, and prints the others", func() {
		missing := filepath.Join(c.Home, "data", "missing.txt") + ":1:x"
		host := filepath.Join(c.Home, "data", "github.com")

		Expect(c.Main("where", missing, filepath.Join(job, "log.txt"), host)).To(Equal(1))
		Expect(strings.Count(c.Stdout.String(), "\n")).To(Equal(1))
		Expect(c.Stderr.String()).To(SatisfyAll(
			ContainSubstring(strconv.Quote(missing)+" names no file"),
			ContainSubstring(host+": github.com is not in a run dir"),
		))
	})

	It("prints each error as it reads stdin", func() {
		var stderr string
		c.Stdin = io.MultiReader(
			strings.NewReader("/nope:3:x\n"),
			onRead(func() { stderr = c.Stderr.String() }),
			strings.NewReader(filepath.Join(job, "log.txt")+"\n"),
		)

		Expect(c.Main("where")).To(Equal(1))
		Expect(stderr).To(Equal("lg: \"/nope:3:x\" names no file\n"))
		Expect(c.Stderr.String()).To(Equal(stderr))
		Expect(decoded()).To(HaveLen(1))
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

		Expect(c.Main("where", log+":10:15:00 ERROR disk full", log+":2:ERROR", log+":2")).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(
			SatisfyAll(Not(HaveKey("line")), HaveKeyWithValue("text", "10:15:00 ERROR disk full")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "ERROR")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), Not(HaveKey("text"))),
		))
	})

	It("exits 1 naming a hit whose text no line of its file holds", func() {
		log := filepath.Join(job, "log.txt")
		Expect(os.WriteFile(log, []byte("start\n10:15:00 ERROR disk full\n"), 0o644)).To(Succeed())
		hits := []string{log + ":1:15:00", log + "-2-ERROR x", log + ":2:ERROR x [... omitted end of long line]"}

		Expect(c.Main(append(append([]string{"where"}, hits...), log+":3")...)).To(Equal(1))
		Expect(c.Stdout.String()).To(BeEmpty())
		for _, hit := range hits {
			Expect(c.Stderr.String()).To(ContainSubstring(strconv.Quote(hit) + ": no line of " + log + " holds its text\n"))
		}
		Expect(c.Stderr.String()).To(ContainSubstring(log + " has no line 3\n"))
	})

	It("prints <, > and & in text unescaped", func() {
		log := filepath.Join(job, "log.txt")
		Expect(os.WriteFile(log, []byte("<nil> & more\n"), 0o644)).To(Succeed())

		Expect(c.Main("where", log+":1:<nil> & more")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(ContainSubstring(`"text":"<nil> & more"`))
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

	It("keeps the line of a hit rg printed with --column or --max-columns", func() {
		log := filepath.Join(job, "log.txt")
		Expect(os.WriteFile(log, []byte("start\n10:15:00 ERROR disk full "+strings.Repeat("y", 200)+"\n"), 0o644)).To(Succeed())

		Expect(c.Main("where",
			log+":2:10:10:15:00 ERROR disk full",
			log+":2:10:15:00 ERROR disk full yyy [... omitted end of long line]",
			log+":2:10:10:15:00 ERROR disk full yyy [... 1 more match]",
			log+":2:[Omitted long matching line]",
			log+":2:10:[Omitted long line with 1 matches]",
		)).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "10:15:00 ERROR disk full")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "10:15:00 ERROR disk full yyy [... omitted end of long line]")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "10:15:00 ERROR disk full yyy [... 1 more match]")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "[Omitted long matching line]")),
			SatisfyAll(HaveKeyWithValue("line", BeEquivalentTo(2)), HaveKeyWithValue("text", "[Omitted long line with 1 matches]")),
		))
	})

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

	It("lists each dir once for hits in it, whatever their text holds", func() {
		text := strings.Repeat("-:", 100)
		log := filepath.Join(job, "log.txt")
		Expect(os.WriteFile(log, []byte(text+"\n"), 0o644)).To(Succeed())
		listed := map[string]int{}
		readDir := func(dir string) ([]fs.DirEntry, error) {
			listed[dir]++
			return os.ReadDir(dir)
		}

		Expect(cli.FindPlaces(filepath.Join(c.Home, "data"), readDir, log+":1:"+text, log+"-1-"+text, log+":"+text)).To(Succeed())
		Expect(listed).To(SatisfyAll(HaveKeyWithValue(job+"/", 1), HaveEach(1)))
	})

	It("decodes a hit in a file created after where listed its dir", func() {
		created := filepath.Join(job, "created.txt")
		c.Stdin = io.MultiReader(
			strings.NewReader(filepath.Join(job, "log.txt")+"\n"),
			onRead(func() { Expect(os.WriteFile(created, []byte("x\n"), 0o644)).To(Succeed()) }),
			strings.NewReader(created+":1:x\n"),
		)

		Expect(c.Main("where")).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveExactElements(HaveKey("path"), HaveKeyWithValue("path", created)))
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

	It("reads a run's files once while it is among the 16 runs hits were last in", func() {
		run := filepath.Dir(attempt)
		log := filepath.Join(job, "log.txt") + "\n"
		var others []io.Reader
		for n := range 16 {
			other := strings.Replace(run, "37129390741_", strconv.Itoa(37129390742+n)+"_", 1)
			Expect(os.CopyFS(other, os.DirFS(run))).To(Succeed())
			others = append(others, strings.NewReader(strings.Replace(log, run, other, 1)))
		}
		jobs := filepath.Join(attempt, "jobs.json")
		good, err := os.ReadFile(jobs)
		Expect(err).NotTo(HaveOccurred())
		breakJobs := onRead(func() { Expect(os.WriteFile(jobs, []byte("{"), 0o644)).To(Succeed()) })
		hits := func(others ...io.Reader) io.Reader {
			Expect(os.WriteFile(jobs, good, 0o644)).To(Succeed())
			for _, r := range others {
				_, err := r.(io.Seeker).Seek(0, io.SeekStart)
				Expect(err).NotTo(HaveOccurred())
			}
			return io.MultiReader(append(append([]io.Reader{strings.NewReader(log), breakJobs}, others...), strings.NewReader(log))...)
		}

		c.Stdin = hits(others[:15]...)
		Expect(c.Main("where")).To(Equal(0), c.Stderr.String())
		Expect(decoded()).To(HaveLen(17))

		c.Stdout.Reset()
		c.Stdin = hits(others...)
		Expect(c.Main("where")).To(Equal(1))
		Expect(decoded()).To(HaveLen(17))
		Expect(c.Stderr.String()).To(ContainSubstring(run + ": cannot read its job"))
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

	It("exits 1 naming the file when another unit of the run does not parse", func() {
		Expect(c.Fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		Expect(c.Main("sync")).To(Equal(0))
		run := filepath.Dir(attempt)
		Expect(os.WriteFile(filepath.Join(attempt, "attempt.json"), []byte("not json"), 0o644)).To(Succeed())

		Expect(c.Main("where",
			filepath.Join(run, "attempt-2", "jobs", "111221662305_build-ubuntu-latest-1.23", "job.json"),
			filepath.Join(run, "artifacts", "11276052917_rerun-only-attempt-2", "artifact.zip"),
		)).To(Equal(1))
		Expect(c.Stdout.String()).To(BeEmpty())
		Expect(strings.Count(c.Stderr.String(), filepath.Join(attempt, "attempt.json"))).To(Equal(2))
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

var _ = Describe("LineReader", Label("where"), func() {
	It("reads each line of a file about once, whatever order it is asked for lines in", func() {
		path := filepath.Join(GinkgoT().TempDir(), "log.txt")
		var lines []string
		for n := range 100 {
			lines = append(lines, fmt.Sprintf("%d %s", n+1, strings.Repeat("x", 8192)))
		}
		content := strings.Join(lines, "\n") + "\n"
		Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
		var read int
		l := cli.NewLineReader(func(path string) (io.ReadSeekCloser, error) {
			file, err := os.Open(path)
			return countingFile{file, &read}, err
		})
		DeferCleanup(l.Close)

		for _, n := range []int{100, 99, 50, 1, 2, 3, 51, 99} {
			line, ok := l.Line(path, n)
			Expect(ok).To(BeTrue())
			Expect(line).To(Equal(lines[n-1]))
		}
		_, ok := l.Line(path, 101)
		Expect(ok).To(BeFalse())
		Expect(read).To(BeNumerically("<", 2*len(content)))
	})

	It("reads each line of a file about once while the file is among the last 16 it read", func() {
		dir := GinkgoT().TempDir()
		var files []string
		content := strings.Repeat(strings.Repeat("x", 1023)+"\n", 100)
		for n := range 17 {
			files = append(files, filepath.Join(dir, strconv.Itoa(n)))
			Expect(os.WriteFile(files[n], []byte(content), 0o644)).To(Succeed())
		}
		// readTwice gives how many bytes reading the last line of each file, twice in turn, reads.
		readTwice := func(files []string) int {
			var read int
			l := cli.NewLineReader(func(path string) (io.ReadSeekCloser, error) {
				file, err := os.Open(path)
				return countingFile{file, &read}, err
			})
			defer l.Close()
			for range 2 {
				for _, file := range files {
					_, ok := l.Line(file, 100)
					Expect(ok).To(BeTrue())
				}
			}
			return read
		}

		Expect(readTwice(files[:16])).To(BeNumerically("<", 16*len(content)*11/10))
		Expect(readTwice(files)).To(BeNumerically(">", 17*len(content)*19/10))
	})
})

var _ = Describe("DirCache", Label("where"), func() {
	It("lists each dir once, however many of its names it is asked about", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "file"), nil, 0o644)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(dir, "dir"), 0o755)).To(Succeed())
		Expect(os.Symlink("dir", filepath.Join(dir, "dir-link"))).To(Succeed())
		Expect(os.Symlink("file", filepath.Join(dir, "file-link"))).To(Succeed())
		Expect(os.Symlink("gone", filepath.Join(dir, "dangling"))).To(Succeed())
		listed := map[string]int{}
		c := cli.NewDirCache(func(dir string) ([]fs.DirEntry, error) {
			listed[dir]++
			return os.ReadDir(dir)
		})
		stat := func(name string) []bool {
			isDir, exists := c.Stat(dir + "/" + name)
			return []bool{isDir, exists}
		}

		for range 2 {
			Expect(stat("file")).To(Equal([]bool{false, true}))
			Expect(stat("dir")).To(Equal([]bool{true, true}))
			Expect(stat("dir-link")).To(Equal([]bool{true, true}))
			Expect(stat("file-link")).To(Equal([]bool{false, true}))
			Expect(stat("dangling")).To(Equal([]bool{false, false}))
			Expect(stat("missing")).To(Equal([]bool{false, false}))
			Expect(stat("missing/file")).To(Equal([]bool{false, false}))
			Expect(stat("dir/.")).To(Equal([]bool{true, true}))
			Expect(stat("dir/..")).To(Equal([]bool{true, true}))
			Expect(stat("file/.")).To(Equal([]bool{false, false}))
		}
		Expect(listed).To(Equal(map[string]int{dir + "/": 1, dir + "/missing/": 2}))
	})
})

// countingFile adds the bytes it reads to *read.
type countingFile struct {
	io.ReadSeekCloser
	read *int
}

func (c countingFile) Read(p []byte) (int, error) {
	n, err := c.ReadSeekCloser.Read(p)
	*c.read += n
	return n, err
}

// onRead is a reader that calls f on its first read, and is empty.
type onRead func()

func (f onRead) Read([]byte) (int, error) {
	f()
	return 0, io.EOF
}
