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

	"github.com/rosenhouse/lg/internal/testsupport/archives"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
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

// recorded lists the manifest's records under key as "archive: name", followed by " (reason)" when one is given.
func recorded(m map[string]any, key string) []string {
	GinkgoHelper()
	Expect(m).To(HaveKey(key))
	var names []string
	for _, r := range m[key].([]any) {
		record := r.(map[string]any)
		name := fmt.Sprintf("%s: %s", record["archive"], record["name"])
		if reason, ok := record["reason"]; ok {
			name += fmt.Sprintf(" (%s)", reason)
		}
		names = append(names, name)
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
		Expect(session.Err.Contents()).To(BeEmpty())
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

	It("extracts every artifact when its reader stops reading after one line", func() {
		Eventually(env.Sh("lg extract --all | head -1"), harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(extractedDirs(env)).To(HaveLen(4))
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
		artifact := artifactDir(env, scenario.ClonedID(a.Release3.ID, passArtifact))
		Expect(hits[0]).To(HavePrefix(filepath.Join(artifact, "extracted", "inner.tar.gz.d") + string(filepath.Separator)))

		Expect(where(env, hits[0])).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("artifact_id", BeEquivalentTo(scenario.ClonedID(a.Release3.ID, passArtifact))),
			HaveKeyWithValue("artifact", "pass-artifact"),
			HaveKeyWithValue("inner_path", "inner.tar.gz.d/tgz/nested.log"),
			HaveKeyWithValue("line", BeEquivalentTo(1)),
		)))
	})
})

var _ = Describe("lg extract", Label("extract"), func() {
	It("selects artifacts by lg paths filters or PATH arguments, skips tombstoned artifacts, and exits 2 asking for a filter, PATH or --all when given none", func() {
		const expired = 11276401837
		main := scenario.Expire(scenario.OnBranch(scenario.CloneAt(9, "after-attempt-1", harness.DefaultNow().Add(-scenario.Day)), "main"), scenario.ClonedID(9, expired))
		other := scenario.OnBranch(scenario.CloneAt(10, "after-attempt-1", harness.DefaultNow().Add(-scenario.Day)), "other")
		env := harness.New(lgPath)
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		Expect(fake.AddRun(main)).To(Succeed())
		Expect(fake.AddRun(other)).To(Succeed())
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		tombstoned := artifactDir(env, scenario.ClonedID(9, expired))
		Expect(filepath.Join(tombstoned, "artifact.zip.tombstone")).To(BeARegularFile())

		none := extract(env)
		Expect(none).To(gexec.Exit(2))
		Expect(none.Err).To(gbytes.Say(regexp.QuoteMeta("lg: extract: give filters, PATHs or --all")))
		Expect(extractedDirs(env)).To(BeEmpty())

		Expect(extract(env, "--branch", "main")).To(gexec.Exit(0))
		mainZips := glob(runDirOf(env, 9), "artifacts", "*", "artifact.zip")
		Expect(mainZips).To(HaveLen(3))
		var mainExtracted []string
		for _, zip := range mainZips {
			mainExtracted = append(mainExtracted, filepath.Join(filepath.Dir(zip), "extracted"))
		}
		Expect(extractedDirs(env)).To(ConsistOf(mainExtracted))

		otherPass := artifactDir(env, scenario.ClonedID(10, passArtifact))
		byPath := extract(env, otherPass, filepath.Join(tombstoned, "artifact.zip.tombstone"))
		Expect(byPath).To(gexec.Exit(0))
		Expect(byPath.Err).To(gbytes.Say(regexp.QuoteMeta(tombstoned)))
		Expect(extractedDirs(env)).To(ConsistOf(append(mainExtracted, filepath.Join(otherPass, "extracted"))))
	})
})

var _ = Describe("lg extract PATH", Label("extract"), func() {
	It("takes a PATH relative to the working dir", func() {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		runDir := filepath.Join(env.Data(), fixtureRunDir)

		session := env.Sh("cd '" + runDir + "' && lg extract artifacts/11276272069_pass-artifact")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(filepath.Join(runDir, "artifacts", "11276272069_pass-artifact", "extracted")).To(BeADirectory())
	})
})

var _ = Describe("lg extract on a crafted archive", Label("extract"), func() {
	It("never writes outside extracted/ for .., absolute, symlink or hardlink entries, skips devices and FIFOs, drops setuid bits, and records each in .lg-extract.json", func() {
		inner := archives.Tar(
			archives.Entry{Name: "ok-in-tar.txt", Body: "kept\n"},
			archives.Entry{Name: "../tar-escape.txt", Body: "escaped\n"},
			archives.Entry{Name: "/tar-absolute.txt", Body: "absolute\n"},
			archives.Entry{Name: "tar-link", TarType: tar.TypeSymlink, Link: "../../../../../../../../etc"},
			archives.Entry{Name: "hard", TarType: tar.TypeLink, Link: "ok-in-tar.txt"},
			archives.Entry{Name: "dev", TarType: tar.TypeChar},
			archives.Entry{Name: "fifo", TarType: tar.TypeFifo},
			archives.Entry{Name: "tar-setuid", Body: "#!/bin/sh\n", Mode: 0o755 | fs.ModeSetuid},
		)
		crafted := archives.Zip(
			archives.Entry{Name: "ok.txt", Body: "kept\n"},
			archives.Entry{Name: "../escape.txt", Body: "escaped\n"},
			archives.Entry{Name: "/absolute.txt", Body: "absolute\n"},
			archives.Entry{Name: "link", Mode: fs.ModeSymlink | 0o777, Link: "../../../../../../../../etc/passwd"},
			archives.Entry{Name: "setuid.sh", Body: "#!/bin/sh\n", Mode: 0o755 | fs.ModeSetuid},
			archives.Entry{Name: "special.tar", Body: string(inner)},
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
			Expect(info.Mode()&(fs.ModeSetuid|fs.ModeSetgid)).To(BeZero(), setuid)
		}
		after := treesnap.Snapshot(env.Store())
		for path := range after {
			if _, ok := before[path]; !ok {
				Expect(path).To(Or(HavePrefix("state/"), HavePrefix(filepath.Join(strings.TrimPrefix(dir, env.Store()+"/"), "extracted"))))
			}
		}

		m := manifest(dir)
		Expect(recorded(m, "skipped")).To(ConsistOf(
			"artifact.zip: ../escape.txt (outside)", "artifact.zip: /absolute.txt (absolute)", "artifact.zip: link (symlink)",
			"special.tar: ../tar-escape.txt (outside)", "special.tar: /tar-absolute.txt (absolute)", "special.tar: tar-link (symlink)",
			"special.tar: hard (hardlink)", "special.tar: dev (device)", "special.tar: fifo (fifo)",
		))
		Expect(recorded(m, "setuid_dropped")).To(ConsistOf("artifact.zip: setuid.sh", "special.tar: tar-setuid"))
	})
})

var _ = Describe("lg extract on an archive with duplicate and case-colliding names", Label("extract"), func() {
	It("keeps every entry under a ~N suffix and records the originals", func() {
		env := harness.New(lgPath)
		dir := syncWithZip(env, archives.Zip(
			archives.Entry{Name: "dup.txt", Body: "first\n"},
			archives.Entry{Name: "dup.txt", Body: "second\n"},
			archives.Entry{Name: "Case.txt", Body: "upper first\n"},
			archives.Entry{Name: "case.txt", Body: "lower\n"},
			archives.Entry{Name: "CASE.TXT", Body: "all upper\n"},
			archives.Entry{Name: "dir/a.txt", Body: "a\n"},
			archives.Entry{Name: "DIR/b.txt", Body: "b\n"},
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
			"artifact.zip: dup.txt (collision)", "artifact.zip: case.txt (collision)", "artifact.zip: CASE.TXT (collision)", "artifact.zip: DIR/b.txt (collision)",
		))
	})
})

var _ = Describe("lg extract --max-bytes", Label("extract"), func() {
	It("stops at the cap (default 1GB) and leaves no extracted/ dir", func() {
		env := harness.New(lgPath)
		dir := syncWithZip(env, archives.Zip(archives.Entry{Name: "big.log", Body: strings.Repeat("x", 1000)}))

		capped := extract(env, "--max-bytes", "999", dir)
		Expect(capped).To(gexec.Exit(1))
		Expect(capped.Err).To(gbytes.Say(regexp.QuoteMeta(dir)))
		Expect(capped.Err).To(gbytes.Say("--max-bytes"))
		Expect(extractedDirs(env)).To(BeEmpty())
		Expect(filesBelow(env.Tmp())).To(BeEmpty())

		help := extract(env, "--help")
		Expect(help).To(gexec.Exit(0))
		Expect(help.Out).To(gbytes.Say(`--max-bytes=1GB `))

		Expect(extract(env, "--max-bytes", "1000", dir)).To(gexec.Exit(0))
		Expect(filepath.Join(dir, "extracted", "big.log")).To(BeARegularFile())
	})
})

var _ = Describe("lg extract while a cycle holds the write lock", Label("extract"), func() {
	It("waits, then publishes", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		release := fake.Hold("artifacts/11276272069/zip")
		sync := env.Lg("sync")
		Eventually(fake.Requests, harness.ExitTimeout).Should(ContainElement(HaveField("Path", HaveSuffix("artifacts/11276272069/zip"))))

		session := env.Lg("extract", "--all")
		Eventually(session.Err, harness.ExitTimeout).Should(gbytes.Say("lg: waiting for "))
		Expect(extractedDirs(env)).To(BeEmpty())

		release()
		Eventually(sync, harness.ExitTimeout).Should(gexec.Exit(0))
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(filepath.Join(env.Data(), fixtureRunDir, "artifacts", "11276272069_pass-artifact", "extracted")).To(BeADirectory())
		Expect(extractedDirs(env)).To(HaveLen(4))
	})
})

// zerosZip gives a zip of one member of size zero bytes.
func zerosZip(name string, size int) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	Expect(err).NotTo(HaveOccurred())
	chunk := make([]byte, 1<<20)
	for written := 0; written < size; written += len(chunk) {
		_, err = f.Write(chunk[:min(len(chunk), size-written)])
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(w.Close()).To(Succeed())
	return buf.Bytes()
}

// staged waits for a file below tmp/.
func staged(env *harness.Env) {
	GinkgoHelper()
	Eventually(func() []string { return filesBelow(env.Tmp()) }, harness.ExitTimeout, "10ms").ShouldNot(BeEmpty())
}

var _ = Describe("lg extract interrupted", Label("extract"), func() {
	It("leaves nothing in tmp/", func() {
		env := harness.New(lgPath)
		dir := syncWithZip(env, zerosZip("zeros.log", 512<<20))

		session := env.Lg("extract", dir)
		staged(env)
		session.Interrupt()
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit())
		Expect(env.Tmp()).To(matchers.BeSwept())
		Expect(filepath.Join(dir, "extracted")).NotTo(BeADirectory())
	})
})

var _ = Describe("lg extract of two artifacts", Label("extract"), func() {
	It("lets a cycle waiting for the write lock run before the second", func() {
		env := harness.New(lgPath)
		pass := syncWithZip(env, zerosZip("zeros.log", 128<<20))
		expiring := artifactDir(env, 11275917910)

		session := env.Lg("extract", pass, expiring)
		staged(env)
		Eventually(env.Sync(), harness.ExitTimeout).Should(gexec.Exit(0))
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(modTime(filepath.Join(env.State(), "status.json"))).To(BeTemporally("<", modTime(filepath.Join(expiring, "extracted"))))
	})
})

func modTime(path string) time.Time {
	GinkgoHelper()
	info, err := os.Stat(path)
	Expect(err).NotTo(HaveOccurred())
	return info.ModTime()
}

var _ = Describe("lg extract past disk_cap", Label("extract"), func() {
	It("warns that the next cycle evicts extracted/ trees", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		env.WriteConfig(fake.URL(), fmt.Sprintf("disk_cap: %d", apparentBytes(env.Data())+1))

		session := extract(env, "--all")
		Expect(session).To(gexec.Exit(0))
		Expect(session.Err).To(gbytes.Say(regexp.QuoteMeta("lg: data/ now exceeds disk_cap; the next cycle evicts extracted/ trees, oldest run first\n")))
		Expect(extractedDirs(env)).To(HaveLen(4))
	})

	It("says nothing when removing expired runs brings data/ under disk_cap", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		Expect(fake.AddRun(scenario.CloneAt(1, "after-attempt-1", harness.DefaultNow().Add(-3*scenario.Day)))).To(Succeed())
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		env.WriteConfig(fake.URL(), "backfill: 2d", "retention: 2d", fmt.Sprintf("disk_cap: %d", apparentBytes(env.Data())))

		session := extract(env, artifactDir(env, passArtifact))
		Expect(session).To(gexec.Exit(0))
		Expect(session.Err.Contents()).To(BeEmpty())
		Expect(outputLines(gc(env, "--dry-run"))).To(ConsistOf(runDir(env, 1)))
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
