package extract_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/extract"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/archives"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
	"github.com/rosenhouse/lg/internal/version"
)

var now = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

type fixture struct {
	root, artifact, extracted string
	fsys                      *faultfs.FS
	s                         *store.Store
}

// newFixture makes a store whose artifact dir holds zip as artifact.zip.
func newFixture(zip []byte) fixture {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	Expect(store.Init(root)).To(Succeed())
	fsys := faultfs.New()
	s, err := store.OpenFS(fsys, root)
	Expect(err).NotTo(HaveOccurred())
	artifact := filepath.Join(root, "data", "github.com", "o", "r", "runs", "2026-10-01", "1_ci_main", "artifacts", "5_report")
	Expect(os.MkdirAll(artifact, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(artifact, "artifact.zip"), zip, 0o644)).To(Succeed())
	return fixture{root: root, artifact: artifact, extracted: filepath.Join(artifact, "extracted"), fsys: fsys, s: s}
}

func (f fixture) extract(limits extract.Limits) error {
	return extract.Extract(f.s, f.artifact, limits, now)
}

// files maps the path of each file below extracted/ to its bytes.
func (f fixture) files() map[string]string {
	GinkgoHelper()
	found := map[string]string{}
	Expect(filepath.WalkDir(f.extracted, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(f.extracted, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		found[filepath.ToSlash(rel)] = string(data)
		return err
	})).To(Succeed())
	return found
}

func (f fixture) manifest() map[string]any {
	GinkgoHelper()
	var m map[string]any
	data, err := os.ReadFile(filepath.Join(f.extracted, ".lg-extract.json"))
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(data, &m)).To(Succeed())
	return m
}

func record(fields ...string) types.GomegaMatcher {
	var matchers []types.GomegaMatcher
	for i := 0; i < len(fields); i += 2 {
		matchers = append(matchers, HaveKeyWithValue(fields[i], fields[i+1]))
	}
	return SatisfyAll(matchers...)
}

var _ = Describe("Extract", Label("extract"), func() {
	It("expands artifact.zip into extracted/, each nested archive beside itself as <archive>.d/, and records the version and time in .lg-extract.json", func() {
		f := newFixture(scenario.BuildArtifactZip("nested text"))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		files := f.files()
		Expect(files).To(HaveKeyWithValue("top.log", "LG_MARKER artifact top-level\n"))
		Expect(files).To(HaveKeyWithValue("inner.zip.d/zip/nested.log", "LG_MARKER artifact nested in zip\n"))
		Expect(files).To(HaveKeyWithValue("inner.tar.gz.d/tgz/nested.log", "nested text\n"))
		Expect(files).To(HaveKey("inner.zip"))
		Expect(files).To(HaveKey("inner.tar.gz"))
		Expect(f.manifest()).To(Equal(map[string]any{
			"lg_format":      1.0,
			"lg_version":     version.Version,
			"extracted_at":   "2026-10-03T18:00:00Z",
			"max_bytes":      1e9,
			"bytes":          written(files),
			"renamed":        []any{},
			"skipped":        []any{},
			"setuid_dropped": []any{},
			"not_expanded":   []any{},
		}))
	})

	It("is staged in tmp/ and published with store.Publish", func() {
		f := newFixture(scenario.BuildArtifactZip("nested text"))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		tmp := filepath.Join(f.root, "tmp") + string(filepath.Separator)
		var renames []faultfs.Op
		for _, op := range f.fsys.Journal() {
			if op.Name == "create" || op.Name == "mkdir" {
				Expect(op.Path).To(HavePrefix(tmp), op.String())
			}
			if op.Name == "rename" {
				renames = append(renames, op)
			}
		}
		Expect(renames).To(HaveExactElements(HaveField("To", f.extracted)))
		Expect(filepath.Join(f.root, "tmp")).To(matchers.BeSwept())
	})

	It("gives every dir it writes owner rwx, so eviction can rename and remove it", func() {
		f := newFixture(scenario.BuildArtifactZip("nested text"))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(filepath.WalkDir(f.extracted, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			info, err := d.Info()
			Expect(info.Mode().Perm() & 0o700).To(Equal(fs.FileMode(0o700)), path)
			return err
		})).To(Succeed())
	})

	It("caps path components at 200 bytes and depth at 32, slugifying offending names and recording the originals", func() {
		long := strings.Repeat("a", 196) + ".log"
		longest := strings.Repeat("b", 197) + ".log"
		deep := strings.Repeat("d/", 31) + "deep.log"
		deeper := strings.Repeat("e/", 32) + "deeper.log"
		f := newFixture(archives.Zip(
			archives.Entry{Name: long, Body: "200 bytes\n"},
			archives.Entry{Name: longest, Body: "201 bytes\n"},
			archives.Entry{Name: deep, Body: "32 deep\n"},
			archives.Entry{Name: deeper, Body: "33 deep\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		slugged := strings.Repeat("b", 60)
		flattened := strings.Repeat("e/", 31) + "e-deeper.log"
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue(long, "200 bytes\n"),
			HaveKeyWithValue(slugged, "201 bytes\n"),
			HaveKeyWithValue(deep, "32 deep\n"),
			HaveKeyWithValue(flattened, "33 deep\n"),
		))
		Expect(f.manifest()["renamed"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", longest, "path", slugged, "reason", "too_long"),
			record("archive", "artifact.zip", "name", deeper, "path", flattened, "reason", "too_deep"),
		))
	})

	It("detects nested archives by magic bytes, whatever their names: zip, gzip+tar and tar; a gzip of no tar stays a file", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "report.bin", Body: string(archives.Zip(archives.Entry{Name: "a.log", Body: "zip\n"}))},
			archives.Entry{Name: "logs.dat", Body: string(archives.TarGz(archives.Entry{Name: "b.log", Body: "tar.gz\n"}))},
			archives.Entry{Name: "plain", Body: string(archives.Tar(archives.Entry{Name: "c.log", Body: "tar\n"}))},
			archives.Entry{Name: "notes.gz", Body: string(archives.Gzip([]byte("just text\n")))},
			archives.Entry{Name: "fake.zip", Body: "not a zip\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue("report.bin.d/a.log", "zip\n"),
			HaveKeyWithValue("logs.dat.d/b.log", "tar.gz\n"),
			HaveKeyWithValue("plain.d/c.log", "tar\n"),
			HaveKey("notes.gz"),
			HaveKeyWithValue("fake.zip", "not a zip\n"),
		))
		Expect(f.files()).NotTo(HaveKey(HavePrefix("notes.gz.d")))
		Expect(f.files()).NotTo(HaveKey(HavePrefix("fake.zip.d")))
	})

	It("expands nested archives to 8 levels; deeper ones stay as files and are recorded", func() {
		body := archives.Zip(archives.Entry{Name: "bottom.log", Body: "bottom\n"})
		for level := 9; level >= 1; level-- {
			body = archives.Zip(archives.Entry{Name: fmt.Sprintf("n%d.zip", level), Body: string(body)})
		}
		f := newFixture(body)

		Expect(f.extract(extract.Defaults())).To(Succeed())
		dir := ""
		for level := 1; level <= 8; level++ {
			dir += fmt.Sprintf("n%d.zip.d/", level)
		}
		Expect(f.files()).To(HaveKey(dir + "n9.zip"))
		Expect(f.files()).NotTo(HaveKey(HavePrefix(dir + "n9.zip.d")))
		Expect(f.manifest()["not_expanded"]).To(ConsistOf(record("archive", strings.TrimSuffix(dir, ".d/"), "name", "n9.zip", "path", dir+"n9.zip", "reason", "nesting")))
	})

	It("keeps a nested archive that does not read as a file, and records why", func() {
		f := newFixture(archives.Zip(archives.Entry{Name: "broken.zip", Body: "PK\x03\x04 truncated"}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(HaveKeyWithValue("broken.zip", "PK\x03\x04 truncated"))
		Expect(f.manifest()["not_expanded"]).To(ConsistOf(SatisfyAll(
			record("archive", "artifact.zip", "name", "broken.zip", "path", "broken.zip"),
			HaveKeyWithValue("reason", ContainSubstring("zip")),
		)))
	})

	It("stops at MaxBytes, counting every file it writes, and leaves no extracted/ and nothing in tmp/", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "a.log", Body: strings.Repeat("a", 600)},
			archives.Entry{Name: "b.tar", Body: string(archives.Tar(archives.Entry{Name: "b.log", Body: strings.Repeat("b", 100)}))},
		))
		limits := extract.Defaults()
		limits.MaxBytes = 600 + int64(len(archives.Tar(archives.Entry{Name: "b.log", Body: strings.Repeat("b", 100)}))) + 99

		Expect(f.extract(limits)).To(MatchError(store.ErrTooLarge))
		Expect(f.extracted).NotTo(BeADirectory())
		Expect(filepath.Join(f.root, "tmp")).To(matchers.BeSwept())

		limits.MaxBytes++
		Expect(f.extract(limits)).To(Succeed())
		Expect(f.manifest()["bytes"]).To(BeEquivalentTo(limits.MaxBytes))
	})

	It("fails on an artifact.zip that does not read as a zip, publishing nothing", func() {
		f := newFixture([]byte("not a zip"))

		Expect(f.extract(extract.Defaults())).To(MatchError(ContainSubstring("artifact.zip")))
		Expect(f.extracted).NotTo(BeADirectory())
		Expect(filepath.Join(f.root, "tmp")).To(matchers.BeSwept())
	})
})

// written sums the sizes of the files below extracted/ but .lg-extract.json.
func written(files map[string]string) float64 {
	var n int
	for name, data := range files {
		if name != ".lg-extract.json" {
			n += len(data)
		}
	}
	return float64(n)
}
