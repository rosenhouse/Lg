package e2e_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

const passArtifact = 11276272069

// extract runs lg extract with args to exit.
func extract(env *harness.Env, args ...string) *gexec.Session {
	GinkgoHelper()
	session := env.Lg(append([]string{"extract"}, args...)...)
	Eventually(session, harness.ExitTimeout).Should(gexec.Exit())
	return session
}

// extractedDirs lists every extracted/ dir under data/.
func extractedDirs(env *harness.Env) []string {
	GinkgoHelper()
	dirs, err := filepath.Glob(filepath.Join(env.Data(), "*", "*", "*", "runs", "*", "*", "artifacts", "*", "extracted"))
	Expect(err).NotTo(HaveOccurred())
	return dirs
}

// artifactDir is the dir of the artifact under data/.
func artifactDir(env *harness.Env, id int64) string {
	GinkgoHelper()
	dirs := glob(env.Data(), "*", "*", "*", "runs", "*", "*", "artifacts", fmt.Sprintf("%d_*", id))
	Expect(dirs).To(HaveLen(1))
	return dirs[0]
}

// manifest decodes the .lg-extract.json of the artifact's extracted/ tree.
func manifest(dir string) map[string]any {
	GinkgoHelper()
	return readJSON(filepath.Join(dir, "extracted", ".lg-extract.json"))
}

// syncWithZip syncs the fixture run at after-attempt-1, its pass-artifact
// serving zip, and gives the artifact's dir.
func syncWithZip(env *harness.Env, zip []byte) string {
	GinkgoHelper()
	fake := fakegithub.New()
	DeferCleanup(fake.Close)
	Expect(fake.AddRun(scenario.WithArtifactZip(scenario.Recorded(fixtureRun, "after-attempt-1"), passArtifact, zip))).To(Succeed())
	env.WriteConfig(fake.URL())
	Expect(env.Sync()).To(gexec.Exit(0))
	return artifactDir(env, passArtifact)
}

type entry struct {
	name     string
	body     string
	mode     fs.FileMode
	typeflag byte
	link     string
}

func zipOf(entries ...entry) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		header.SetMode(mode)
		f, err := w.CreateHeader(header)
		Expect(err).NotTo(HaveOccurred())
		body := e.body
		if mode&fs.ModeSymlink != 0 {
			body = e.link
		}
		_, err = f.Write([]byte(body))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(w.Close()).To(Succeed())
	return buf.Bytes()
}

func tarOf(entries ...entry) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := int64(e.mode.Perm())
		if e.mode&fs.ModeSetuid != 0 {
			mode |= 0o4000
		}
		if mode == 0 {
			mode = 0o644
		}
		header := &tar.Header{Name: e.name, Typeflag: typeflag, Mode: mode, Linkname: e.link, ModTime: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
		if typeflag == tar.TypeReg {
			header.Size = int64(len(e.body))
		}
		Expect(w.WriteHeader(header)).To(Succeed())
		_, err := w.Write([]byte(e.body))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(w.Close()).To(Succeed())
	return buf.Bytes()
}

// filesBelow lists the paths below dir, relative to it, of every entry that is not a dir.
func filesBelow(dir string) []string {
	GinkgoHelper()
	var found []string
	Expect(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, relErr := filepath.Rel(dir, path)
			found = append(found, rel)
			return relErr
		}
		return err
	})).To(Succeed())
	return found
}

// recorded lists the manifest's records under key as "archive: name".
func recorded(m map[string]any, key string) []string {
	GinkgoHelper()
	Expect(m).To(HaveKey(key))
	var names []string
	for _, r := range m[key].([]any) {
		record := r.(map[string]any)
		names = append(names, fmt.Sprintf("%s: %s", record["archive"], record["name"]))
	}
	return names
}

var _ = Describe("lg extract --all after syncing after-attempt-1", Label("extract"), func() {
	var (
		env  *harness.Env
		pass string
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		pass = filepath.Join(env.Data(), fixtureRunDir, "artifacts", "11276272069_pass-artifact")
	})

	It("creates artifacts/11276272069_pass-artifact/extracted/ with top.log, inner.zip.d/zip/nested.log and inner.tar.gz.d/tgz/nested.log", func() {
		session := extract(env, "--all")
		Expect(session).To(gexec.Exit(0))
		extracted := filepath.Join(pass, "extracted")
		Expect(outputLines(session)).To(ContainElement(extracted))
		Expect(os.ReadFile(filepath.Join(extracted, "top.log"))).To(Equal([]byte("LG_MARKER artifact top-level attempt=1\n")))
		Expect(os.ReadFile(filepath.Join(extracted, "inner.zip.d", "zip", "nested.log"))).To(Equal([]byte("LG_MARKER artifact nested in zip\n")))
		Expect(os.ReadFile(filepath.Join(extracted, "inner.tar.gz.d", "tgz", "nested.log"))).To(Equal([]byte("LG_MARKER artifact nested in tar.gz\n")))
		Expect(filepath.Join(extracted, ".lg-extract.json")).To(BeARegularFile())
	})

	It("makes `lg paths --unit extracted -0 | xargs -0 -r grep -l 'LG_MARKER artifact nested in tar.gz'` exit 0 printing the nested file", func() {
		Expect(extract(env, "--all")).To(gexec.Exit(0))

		grep := env.Sh("lg paths --unit extracted -0 | xargs -0 -r grep -l 'LG_MARKER artifact nested in tar.gz'")
		Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(outputLines(grep)).To(ConsistOf(filepath.Join(pass, "extracted", "inner.tar.gz.d", "tgz", "nested.log")))
	})

	It("keeps `lg paths -0 | xargs -0 -r grep -l LG_MARKER` exiting 0 with extracted trees in scope", func() {
		Expect(extract(env, "--all")).To(gexec.Exit(0))

		grep := env.Sh("lg paths -0 | xargs -0 -r grep -l LG_MARKER")
		Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
		extracted := filepath.Join(pass, "extracted")
		Expect(outputLines(grep)).To(ContainElements(
			filepath.Join(extracted, "top.log"),
			filepath.Join(extracted, "inner.zip.d", "zip", "nested.log"),
			filepath.Join(extracted, "inner.tar.gz.d", "tgz", "nested.log"),
			passLogOf(env),
		))
	})

	It("leaves artifact.zip byte-identical, and a second run changes nothing", func() {
		zip := filepath.Join(pass, "artifact.zip")
		before, err := os.ReadFile(zip)
		Expect(err).NotTo(HaveOccurred())

		Expect(extract(env, "--all")).To(gexec.Exit(0))
		Expect(os.ReadFile(zip)).To(Equal(before))
		Expect(extractedDirs(env)).NotTo(BeEmpty())
		snap := treesnap.Snapshot(env.Data())

		second := extract(env, "--all")
		Expect(second).To(gexec.Exit(0))
		Expect(second.Out.Contents()).To(BeEmpty())
		Expect(treesnap.Snapshot(env.Data())).To(Equal(snap))
	})
})

// passLogOf is a log of the fixture run that holds LG_MARKER.
func passLogOf(env *harness.Env) string {
	GinkgoHelper()
	logs := glob(env.Data(), fixtureRunDir, "attempt-1", "jobs", "111221289888_*", "log.txt")
	Expect(logs).To(HaveLen(1))
	return logs[0]
}

var _ = Describe("lg extract --branch release-3", Label("extract"), func() {
	It("finds 'foo bar' inside the nested tar.gz of the Archaeology release-3 artifact, and lg where decodes the hit to artifact id and inner path", func() {
		env := archaeologyEnv()
		a := scenario.Archaeology()

		Expect(extract(env, "--branch", "release-3")).To(gexec.Exit(0))
		for _, dir := range extractedDirs(env) {
			Expect(dir).To(under(runDirOf(env, a.Release3.ID)))
		}

		grep := env.Sh("lg paths --unit extracted -0 | xargs -0 -r grep -Hn 'foo bar'")
		Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
		hits := outputLines(grep)
		Expect(hits).To(HaveLen(1))
		artifact := artifactDir(env, a.Release3.ID*1_000_000_000_000+passArtifact)
		Expect(hits[0]).To(HavePrefix(filepath.Join(artifact, "extracted", "inner.tar.gz.d") + string(filepath.Separator)))

		Expect(where(env, hits[0])).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("artifact_id", BeEquivalentTo(a.Release3.ID*1_000_000_000_000+passArtifact)),
			HaveKeyWithValue("artifact", "pass-artifact"),
			HaveKeyWithValue("inner_path", "inner.tar.gz.d/tgz/nested.log"),
			HaveKeyWithValue("line", BeEquivalentTo(1)),
		)))
	})
})

var _ = Describe("lg extract", Label("extract"), func() {
	It("selects artifacts by lg paths filters or PATH arguments, skips tombstoned artifacts, and exits 2 asking for a filter, PATH or --all when given none", func() {
		const expired = 11276401837
		main := scenario.Expire(scenario.OnBranch(scenario.CloneAt(9, "after-attempt-1", harness.DefaultNow().Add(-scenario.Day)), "main"), 9_000_000_000_000+expired)
		other := scenario.OnBranch(scenario.CloneAt(10, "after-attempt-1", harness.DefaultNow().Add(-scenario.Day)), "other")
		env := harness.New(lgPath)
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		Expect(fake.AddRun(main)).To(Succeed())
		Expect(fake.AddRun(other)).To(Succeed())
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		tombstoned := artifactDir(env, 9_000_000_000_000+expired)
		Expect(filepath.Join(tombstoned, "artifact.zip.tombstone")).To(BeARegularFile())

		none := extract(env)
		Expect(none).To(gexec.Exit(2))
		Expect(none.Err).To(gbytes.Say(regexp.QuoteMeta("lg: give filters, PATHs or --all")))
		Expect(extractedDirs(env)).To(BeEmpty())

		Expect(extract(env, "--branch", "main")).To(gexec.Exit(0))
		mainZips := glob(runDirOf(env, 9), "artifacts", "*", "artifact.zip")
		Expect(mainZips).To(HaveLen(3))
		var mainExtracted []string
		for _, zip := range mainZips {
			mainExtracted = append(mainExtracted, filepath.Join(filepath.Dir(zip), "extracted"))
		}
		Expect(extractedDirs(env)).To(ConsistOf(mainExtracted))

		otherPass := artifactDir(env, 10_000_000_000_000+passArtifact)
		byPath := extract(env, otherPass, filepath.Join(tombstoned, "artifact.zip.tombstone"))
		Expect(byPath).To(gexec.Exit(0))
		Expect(byPath.Err).To(gbytes.Say(regexp.QuoteMeta(tombstoned)))
		Expect(extractedDirs(env)).To(ConsistOf(append(mainExtracted, filepath.Join(otherPass, "extracted"))))
	})
})

var _ = Describe("lg extract on a crafted archive", Label("extract"), func() {
	It("never writes outside extracted/ for .., absolute, symlink or hardlink entries, skips devices and FIFOs, drops setuid bits, and records each in .lg-extract.json", func() {
		inner := tarOf(
			entry{name: "ok-in-tar.txt", body: "kept\n"},
			entry{name: "../tar-escape.txt", body: "escaped\n"},
			entry{name: "/tar-absolute.txt", body: "absolute\n"},
			entry{name: "tar-link", typeflag: tar.TypeSymlink, link: "../../../../../../../../etc"},
			entry{name: "hard", typeflag: tar.TypeLink, link: "ok-in-tar.txt"},
			entry{name: "dev", typeflag: tar.TypeChar},
			entry{name: "fifo", typeflag: tar.TypeFifo},
			entry{name: "tar-setuid", body: "#!/bin/sh\n", mode: 0o755 | fs.ModeSetuid},
		)
		crafted := zipOf(
			entry{name: "ok.txt", body: "kept\n"},
			entry{name: "../escape.txt", body: "escaped\n"},
			entry{name: "/absolute.txt", body: "absolute\n"},
			entry{name: "link", mode: fs.ModeSymlink | 0o777, link: "../../../../../../../../etc/passwd"},
			entry{name: "setuid.sh", body: "#!/bin/sh\n", mode: 0o755 | fs.ModeSetuid},
			entry{name: "special.tar", body: string(inner)},
		)
		env := harness.New(lgPath)
		dir := syncWithZip(env, crafted)
		before := treesnap.Snapshot(env.Store())

		Expect(extract(env, dir)).To(gexec.Exit(0))
		extracted := filepath.Join(dir, "extracted")
		Expect(filesBelow(extracted)).To(ConsistOf(
			".lg-extract.json", "ok.txt", "setuid.sh", "special.tar",
			filepath.Join("special.tar.d", "ok-in-tar.txt"), filepath.Join("special.tar.d", "tar-setuid"),
		))
		for _, setuid := range []string{"setuid.sh", filepath.Join("special.tar.d", "tar-setuid")} {
			info, err := os.Stat(filepath.Join(extracted, setuid))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode() & (fs.ModeSetuid | fs.ModeSetgid)).To(BeZero(), setuid)
		}
		after := treesnap.Snapshot(env.Store())
		for path := range after {
			if _, ok := before[path]; !ok {
				Expect(path).To(Or(HavePrefix("state/"), HavePrefix(filepath.Join(strings.TrimPrefix(dir, env.Store()+"/"), "extracted"))))
			}
		}

		m := manifest(dir)
		Expect(recorded(m, "skipped")).To(ConsistOf(
			"artifact.zip: ../escape.txt", "artifact.zip: /absolute.txt", "artifact.zip: link",
			"special.tar: ../tar-escape.txt", "special.tar: /tar-absolute.txt", "special.tar: tar-link",
			"special.tar: hard", "special.tar: dev", "special.tar: fifo",
		))
		Expect(recorded(m, "setuid_dropped")).To(ConsistOf("artifact.zip: setuid.sh", "special.tar: tar-setuid"))
	})
})

var _ = Describe("lg extract on an archive with duplicate and case-colliding names", Label("extract"), func() {
	It("keeps every entry under a ~N suffix and records the originals", func() {
		env := harness.New(lgPath)
		dir := syncWithZip(env, zipOf(
			entry{name: "dup.txt", body: "first\n"},
			entry{name: "dup.txt", body: "second\n"},
			entry{name: "Case.txt", body: "upper first\n"},
			entry{name: "case.txt", body: "lower\n"},
			entry{name: "CASE.TXT", body: "all upper\n"},
			entry{name: "dir/a.txt", body: "a\n"},
			entry{name: "DIR/b.txt", body: "b\n"},
		))

		Expect(extract(env, dir)).To(gexec.Exit(0))
		extracted := filepath.Join(dir, "extracted")
		for name, body := range map[string]string{
			"dup.txt": "first\n", "dup.txt~1": "second\n",
			"Case.txt": "upper first\n", "case.txt~1": "lower\n", "CASE.TXT~2": "all upper\n",
			"dir/a.txt": "a\n", "DIR~1/b.txt": "b\n",
		} {
			Expect(os.ReadFile(filepath.Join(extracted, name))).To(Equal([]byte(body)), name)
		}
		Expect(recorded(manifest(dir), "renamed")).To(ConsistOf(
			"artifact.zip: dup.txt", "artifact.zip: case.txt", "artifact.zip: CASE.TXT", "artifact.zip: DIR/b.txt",
		))
	})
})

var _ = Describe("lg extract --max-bytes", Label("extract"), func() {
	It("stops at the cap (default 1GB) and leaves no extracted/ dir", func() {
		env := harness.New(lgPath)
		dir := syncWithZip(env, zipOf(entry{name: "big.log", body: strings.Repeat("x", 1000)}))

		capped := extract(env, "--max-bytes", "999", dir)
		Expect(capped).To(gexec.Exit(1))
		Expect(capped.Err).To(gbytes.Say(regexp.QuoteMeta(dir)))
		Expect(capped.Err).To(gbytes.Say("--max-bytes"))
		Expect(extractedDirs(env)).To(BeEmpty())
		Expect(filesBelow(env.Tmp())).To(BeEmpty())

		help := extract(env, "--help")
		Expect(help).To(gexec.Exit(0))
		Expect(help.Out).To(gbytes.Say(`--max-bytes=.*\(default: 1GB\)`))

		Expect(extract(env, "--max-bytes", "1000", dir)).To(gexec.Exit(0))
		Expect(filepath.Join(dir, "extracted", "big.log")).To(BeARegularFile())
	})
})

var _ = Describe("lg extract while a cycle holds the write lock", Label("extract"), func() {
	It("waits, then publishes", func() {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		held, err := lock.Wait(filepath.Join(env.State(), "write.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())

		session := env.Lg("extract", "--all")
		Eventually(session.Err, harness.ExitTimeout).Should(gbytes.Say("lg: waiting for "))
		Consistently(session, time.Second).ShouldNot(gexec.Exit())
		Expect(extractedDirs(env)).To(BeEmpty())

		Expect(held.Release()).To(Succeed())
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(extractedDirs(env)).To(HaveLen(4))
	})
})

var _ = Describe("lg gc over disk_cap", Label("extract"), func() {
	It("evicts real lg extract output before any run dir", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(extract(env, "--all")).To(gexec.Exit(0))
		trees := extractedDirs(env)
		Expect(trees).To(HaveLen(4))
		env.WriteConfig(fake.URL(), fmt.Sprintf("disk_cap: %d", apparentBytes(env.Data())-1))

		Expect(gc(env)).To(gexec.Exit(0))
		Expect(extractedDirs(env)).To(HaveLen(3))
		Expect(filepath.Join(env.Data(), fixtureRunDir)).To(BeADirectory())
		Expect(glob(env.Data(), fixtureRunDir, "attempt-1", "jobs", "*", "log.txt")).To(HaveLen(10))
	})
})
