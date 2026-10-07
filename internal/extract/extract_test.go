package extract_test

import (
	"archive/tar"
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
			Expect(info.Mode().Perm()&0o700).To(Equal(fs.FileMode(0o700)), path)
			return err
		})).To(Succeed())
	})

	It("caps path components at 200 bytes and depth at 32, cutting offending names short of their extension and recording the originals", func() {
		long := strings.Repeat("a", 196) + ".log"
		longest := strings.Repeat("b", 197) + ".log"
		wide := strings.Repeat("テ", 86) + ".log"
		deep := strings.Repeat("d/", 31) + "deep.log"
		deeper := strings.Repeat("e/", 32) + "deeper.log"
		f := newFixture(archives.Zip(
			archives.Entry{Name: long, Body: "200 bytes\n"},
			archives.Entry{Name: longest, Body: "201 bytes\n"},
			archives.Entry{Name: wide, Body: "262 bytes\n"},
			archives.Entry{Name: deep, Body: "32 deep\n"},
			archives.Entry{Name: deeper, Body: "33 deep\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		cut := strings.Repeat("b", 196) + ".log"
		wideCut := strings.Repeat("テ", 65) + ".log"
		flattened := strings.Repeat("e/", 31) + "e-deeper.log"
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue(long, "200 bytes\n"),
			HaveKeyWithValue(cut, "201 bytes\n"),
			HaveKeyWithValue(wideCut, "262 bytes\n"),
			HaveKeyWithValue(deep, "32 deep\n"),
			HaveKeyWithValue(flattened, "33 deep\n"),
		))
		Expect(f.manifest()["renamed"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", longest, "path", cut, "reason", "too_long"),
			record("archive", "artifact.zip", "name", wide, "path", wideCut, "reason", "too_long"),
			record("archive", "artifact.zip", "name", deeper, "path", flattened, "reason", "too_deep"),
		))
	})

	It("caps a member's path below extracted/ at 512 bytes, joining the rest of it into one name that keeps its extension", func() {
		var dirs []string
		for i := range 20 {
			dirs = append(dirs, fmt.Sprintf("d%02d", i)+strings.Repeat("x", 197))
		}
		long := strings.Join(dirs, "/") + "/deep.log"
		f := newFixture(archives.Zip(archives.Entry{Name: "ok.log", Body: "ok\n"}, archives.Entry{Name: long, Body: "deep\n"}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		collapsed := dirs[0] + "/" + dirs[1] + "/d02" + strings.Repeat("x", 103) + ".log"
		Expect(f.files()).To(SatisfyAll(HaveLen(3), HaveKeyWithValue("ok.log", "ok\n"), HaveKeyWithValue(collapsed, "deep\n")))
		Expect(f.manifest()["renamed"]).To(ConsistOf(record("archive", "artifact.zip", "name", long, "path", collapsed, "reason", "too_long")))
	})

	It("caps the dir a nested archive expands into at 200 bytes, and expands no archive whose files could not fit in 512", func() {
		nested := string(archives.Zip(archives.Entry{Name: "a.log", Body: "a\n"}))
		named200 := strings.Repeat("n", 196) + ".zip"
		wide := strings.Repeat("w", 200)
		tooLong := wide + "/" + wide + "/" + strings.Repeat("z", 45) + ".zip"
		f := newFixture(archives.Zip(archives.Entry{Name: named200, Body: nested}, archives.Entry{Name: tooLong, Body: nested}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		dir := named200[:198] + ".d"
		Expect(f.files()).To(SatisfyAll(HaveLen(4), HaveKeyWithValue(dir+"/a.log", "a\n"), HaveKey(tooLong)))
		Expect(f.manifest()["renamed"]).To(ConsistOf(record("archive", "artifact.zip", "name", named200+".d", "path", dir, "reason", "too_long")))
		Expect(f.manifest()["not_expanded"]).To(ConsistOf(record("archive", "artifact.zip", "name", tooLong, "path", tooLong, "reason", "too_long")))
	})

	It("slugifies a name holding a control byte or invalid UTF-8, which a filesystem or lg paths may refuse", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "dir/bad\xff.log", Body: "utf8\n"},
			archives.Entry{Name: "nul\x00.log", Body: "nul\n"},
			archives.Entry{Name: "new\nline.log", Body: "newline\n"},
			archives.Entry{Name: "del\x7f.log", Body: "del\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue("dir/bad-.log", "utf8\n"), HaveKeyWithValue("nul-.log", "nul\n"),
			HaveKeyWithValue("new-line.log", "newline\n"), HaveKeyWithValue("del-.log", "del\n"),
		))
		Expect(f.manifest()["renamed"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", "dir/bad\ufffd.log", "path", "dir/bad-.log", "reason", "invalid"),
			record("archive", "artifact.zip", "name", "nul\x00.log", "path", "nul-.log", "reason", "invalid"),
			record("archive", "artifact.zip", "name", "new\nline.log", "path", "new-line.log", "reason", "invalid"),
			record("archive", "artifact.zip", "name", "del\x7f.log", "path", "del-.log", "reason", "invalid"),
		))
	})

	It("counts the depth of a nested archive's members from extracted/", func() {
		deep := strings.Repeat("d/", 30) + "deep.log"
		deeper := strings.Repeat("e/", 31) + "deeper.log"
		f := newFixture(archives.Zip(archives.Entry{Name: "n.zip", Body: string(archives.Zip(
			archives.Entry{Name: deep, Body: "32 deep\n"},
			archives.Entry{Name: deeper, Body: "33 deep\n"},
		))}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue("n.zip.d/"+deep, "32 deep\n"),
			HaveKeyWithValue("n.zip.d/"+strings.Repeat("e/", 30)+"e-deeper.log", "33 deep\n"),
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
		Expect(f.manifest()["not_expanded"]).To(BeEmpty())
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

	It("expands a nested archive only when its files would be at most 32 deep", func() {
		nested := string(archives.Zip(archives.Entry{Name: "a.log", Body: "a\n"}))
		shallow := strings.Repeat("s/", 30) + "x.zip"
		deep := strings.Repeat("d/", 31) + "x.zip"
		f := newFixture(archives.Zip(archives.Entry{Name: shallow, Body: nested}, archives.Entry{Name: deep, Body: nested}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(HaveKeyWithValue(shallow+".d/a.log", "a\n"))
		Expect(f.files()).To(HaveKey(deep))
		Expect(f.files()).NotTo(HaveKey(HavePrefix(deep + ".d")))
		Expect(f.manifest()["not_expanded"]).To(ConsistOf(record("archive", "artifact.zip", "name", deep, "path", deep, "reason", "too_deep")))
	})

	DescribeTable("skips, and records why, a member that is not a regular file inside extracted/",
		func(entry archives.Entry, reason string) {
			f := newFixture(archives.Zip(
				archives.Entry{Name: "ok.txt", Body: "kept\n"},
				archives.Entry{Name: "member.tar", Body: string(archives.Tar(entry))},
			))

			Expect(f.extract(extract.Defaults())).To(Succeed())
			Expect(f.files()).To(SatisfyAll(HaveLen(3), HaveKey("ok.txt"), HaveKey("member.tar"), HaveKey(".lg-extract.json")))
			Expect(f.manifest()["skipped"]).To(ConsistOf(record("archive", "member.tar", "name", entry.Name, "reason", reason)))
		},
		Entry("a .. path", archives.Entry{Name: "a/../../escape.txt", Body: "x\n"}, "outside"),
		Entry("an absolute path", archives.Entry{Name: "/absolute.txt", Body: "x\n"}, "absolute"),
		Entry("a symlink", archives.Entry{Name: "link", Mode: fs.ModeSymlink | 0o777, Link: "/etc/passwd"}, "symlink"),
		Entry("a hardlink", archives.Entry{Name: "hard", TarType: tar.TypeLink, Link: "ok.txt"}, "hardlink"),
		Entry("a char device", archives.Entry{Name: "dev", Mode: fs.ModeDevice | fs.ModeCharDevice | 0o600}, "device"),
		Entry("a block device", archives.Entry{Name: "blk", Mode: fs.ModeDevice | 0o600}, "device"),
		Entry("a FIFO", archives.Entry{Name: "fifo", Mode: fs.ModeNamedPipe | 0o600}, "fifo"),
	)

	DescribeTable("writes a tar member that Go's tar reader serves as regular data",
		func(typeflag byte) {
			f := newFixture(archives.Zip(archives.Entry{Name: "member.tar", Body: string(archives.Tar(
				archives.Entry{Name: "data.log", TarType: typeflag, Body: "LG_MARKER data\n"},
			))}))

			Expect(f.extract(extract.Defaults())).To(Succeed())
			Expect(f.files()).To(HaveKeyWithValue("member.tar.d/data.log", "LG_MARKER data\n"))
			Expect(f.manifest()["skipped"]).To(BeEmpty())
		},
		Entry("a GNU sparse file", byte(tar.TypeGNUSparse)),
		Entry("a contiguous file", byte(tar.TypeCont)),
	)

	It("skips, and records, a zip's symlink, device and FIFO members", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "link", Mode: fs.ModeSymlink | 0o777, Link: "../../../etc/passwd"},
			archives.Entry{Name: "dev", Mode: fs.ModeDevice | 0o600},
			archives.Entry{Name: "fifo", Mode: fs.ModeNamedPipe | 0o600},
			archives.Entry{Name: "socket", Mode: fs.ModeSocket | 0o600},
			archives.Entry{Name: "../escape.txt", Body: "x\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(ConsistOf(Not(BeEmpty())))
		Expect(f.manifest()["skipped"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", "link", "reason", "symlink"),
			record("archive", "artifact.zip", "name", "dev", "reason", "device"),
			record("archive", "artifact.zip", "name", "fifo", "reason", "fifo"),
			record("archive", "artifact.zip", "name", "socket", "reason", "special"),
			record("archive", "artifact.zip", "name", "../escape.txt", "reason", "outside"),
		))
	})

	It("skips dir entries, recording those that carry data", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "logs/", Mode: fs.ModeDir | 0o755},
			archives.Entry{Name: "logs/a.log", Body: "a\n"},
			archives.Entry{Name: "dirmode", Mode: fs.ModeDir | 0o755, Body: "hidden\n"},
			archives.Entry{Name: "member.tar", Body: string(archives.Tar(
				archives.Entry{Name: "tlogs/", Mode: fs.ModeDir | 0o755},
				archives.Entry{Name: "tlogs/b.log", Body: "b\n"},
			))},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(HaveLen(4), HaveKeyWithValue("logs/a.log", "a\n"), HaveKeyWithValue("member.tar.d/tlogs/b.log", "b\n")))
		Expect(f.manifest()["skipped"]).To(ConsistOf(record("archive", "artifact.zip", "name", "dirmode", "reason", "dir_with_data")))
	})

	It("writes setuid and setgid members without those bits, and records them", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "setuid", Body: "#!/bin/sh\n", Mode: fs.ModeSetuid | 0o755},
			archives.Entry{Name: "setgid", Body: "#!/bin/sh\n", Mode: fs.ModeSetgid | 0o755},
			archives.Entry{Name: "plain", Body: "#!/bin/sh\n", Mode: 0o755},
			archives.Entry{Name: "member.tar", Body: string(archives.Tar(archives.Entry{Name: "setgid", Body: "x\n", Mode: fs.ModeSetgid | 0o755}))},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		for _, name := range []string{"setuid", "setgid", "plain", "member.tar.d/setgid"} {
			info, err := os.Stat(filepath.Join(f.extracted, name))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode()&(fs.ModeSetuid|fs.ModeSetgid)).To(BeZero(), name)
		}
		Expect(f.manifest()["setuid_dropped"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", "setuid", "path", "setuid"),
			record("archive", "artifact.zip", "name", "setgid", "path", "setgid"),
			record("archive", "member.tar", "name", "setgid", "path", "member.tar.d/setgid"),
		))
	})

	It("keeps every member whose name another took, even in another case, under a ~N suffix that keeps it within 200 bytes and whole runes", func() {
		long := strings.Repeat("l", 200)
		fits := strings.Repeat("f", 198)
		accented := strings.Repeat("a", 197) + "\u00e9"
		f := newFixture(archives.Zip(
			archives.Entry{Name: "dup.txt", Body: "1\n"},
			archives.Entry{Name: "dup.txt", Body: "2\n"},
			archives.Entry{Name: "Case.txt", Body: "3\n"},
			archives.Entry{Name: "case.txt", Body: "4\n"},
			archives.Entry{Name: "CASE.TXT", Body: "5\n"},
			archives.Entry{Name: "dir/a.txt", Body: "6\n"},
			archives.Entry{Name: "dir/b.txt", Body: "7\n"},
			archives.Entry{Name: "DIR/c.txt", Body: "8\n"},
			archives.Entry{Name: "x", Body: "9\n"},
			archives.Entry{Name: "x/y", Body: "10\n"},
			archives.Entry{Name: ".lg-extract.json", Body: "11\n"},
			archives.Entry{Name: long, Body: "12\n"},
			archives.Entry{Name: long, Body: "13\n"},
			archives.Entry{Name: accented, Body: "14\n"},
			archives.Entry{Name: accented, Body: "15\n"},
			archives.Entry{Name: fits, Body: "16\n"},
			archives.Entry{Name: fits, Body: "17\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue("dup.txt", "1\n"), HaveKeyWithValue("dup.txt~1", "2\n"),
			HaveKeyWithValue("Case.txt", "3\n"), HaveKeyWithValue("case.txt~1", "4\n"), HaveKeyWithValue("CASE.TXT~2", "5\n"),
			HaveKeyWithValue("dir/a.txt", "6\n"), HaveKeyWithValue("dir/b.txt", "7\n"), HaveKeyWithValue("DIR~1/c.txt", "8\n"),
			HaveKeyWithValue("x", "9\n"), HaveKeyWithValue("x~1/y", "10\n"),
			HaveKeyWithValue(".lg-extract.json~1", "11\n"),
			HaveKeyWithValue(long, "12\n"), HaveKeyWithValue(long[:198]+"~1", "13\n"),
			HaveKeyWithValue(accented, "14\n"), HaveKeyWithValue(accented[:197]+"~1", "15\n"),
			HaveKeyWithValue(fits, "16\n"), HaveKeyWithValue(fits+"~1", "17\n"),
		))
		Expect(f.manifest()["renamed"]).To(HaveLen(9))
		Expect(f.manifest()["renamed"]).To(HaveEach(HaveKeyWithValue("reason", "collision")))
	})

	It("keeps the members of a dir whose name another took together under one ~N", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "Logs/a.log", Body: "a\n"},
			archives.Entry{Name: "logs/0.log", Body: "0\n"},
			archives.Entry{Name: "logs/1.log", Body: "1\n"},
			archives.Entry{Name: "f", Body: "f\n"},
			archives.Entry{Name: "f/x.log", Body: "x\n"},
			archives.Entry{Name: "f/y.log", Body: "y\n"},
			archives.Entry{Name: "n.zip", Body: string(archives.Zip(archives.Entry{Name: "n.log", Body: "n\n"}))},
			archives.Entry{Name: "n.zip.d/a", Body: "na\n"},
			archives.Entry{Name: "n.zip.d/b", Body: "nb\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue("Logs/a.log", "a\n"), HaveKeyWithValue("logs~1/0.log", "0\n"), HaveKeyWithValue("logs~1/1.log", "1\n"),
			HaveKeyWithValue("f", "f\n"), HaveKeyWithValue("f~1/x.log", "x\n"), HaveKeyWithValue("f~1/y.log", "y\n"),
			HaveKeyWithValue("n.zip.d/n.log", "n\n"), HaveKeyWithValue("n.zip.d~1/a", "na\n"), HaveKeyWithValue("n.zip.d~1/b", "nb\n"),
		))
		Expect(f.files()).To(HaveLen(11))
	})

	It("names many members of one name in time linear in their number", func(SpecContext) {
		names := make([]string, 20_000)
		for i := range names {
			names[i] = "dup.log"
		}

		paths := extract.PlaceFiles(names...)
		Expect(paths[len(paths)-1]).To(Equal(fmt.Sprintf("dup.log~%d", len(names)-1)))
	}, SpecTimeout(5*time.Second))

	It("keeps a member whose name another took in another Unicode normalization, as APFS folds them", func() {
		nfc, nfd := "caf\u00e9.log", "cafe\u0301.log"
		f := newFixture(archives.Zip(archives.Entry{Name: nfc, Body: "nfc\n"}, archives.Entry{Name: nfd, Body: "nfd\n"}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(HaveKeyWithValue(nfc, "nfc\n"), HaveKeyWithValue(nfd+"~1", "nfd\n")))
		Expect(f.manifest()["renamed"]).To(ConsistOf(record("archive", "artifact.zip", "name", nfd, "path", nfd+"~1", "reason", "collision")))
	})

	It("expands a nested archive into <archive>.d~N when a member took <archive>.d", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "a.zip.d/a.log", Body: "member\n"},
			archives.Entry{Name: "a.zip", Body: string(archives.Zip(archives.Entry{Name: "a.log", Body: "nested\n"}))},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(HaveKeyWithValue("a.zip.d/a.log", "member\n"), HaveKeyWithValue("a.zip.d~1/a.log", "nested\n")))
		Expect(f.manifest()["renamed"]).To(ConsistOf(record("archive", "artifact.zip", "name", "a.zip.d", "path", "a.zip.d~1", "reason", "collision")))
	})

	It("records a member whose name it cleans, but not one that only starts with ./", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "x/../y.txt", Body: "y\n"},
			archives.Entry{Name: "a/./b//c.txt", Body: "c\n"},
			archives.Entry{Name: "./ok.txt", Body: "ok\n"},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(HaveKeyWithValue("y.txt", "y\n"), HaveKeyWithValue("a/b/c.txt", "c\n"), HaveKeyWithValue("ok.txt", "ok\n")))
		Expect(f.manifest()["renamed"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", "x/../y.txt", "path", "y.txt", "reason", "normalized"),
			record("archive", "artifact.zip", "name", "a/./b//c.txt", "path", "a/b/c.txt", "reason", "normalized"),
		))
	})

	It("names a member whose name is only dots and slashes none", func() {
		f := newFixture(archives.Zip(archives.Entry{Name: "member.tar", Body: string(archives.Tar(archives.Entry{Name: ".", TarType: tar.TypeReg, Body: "dot\n"}))}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(HaveKeyWithValue("member.tar.d/none", "dot\n"))
		Expect(f.manifest()["renamed"]).To(ConsistOf(record("archive", "member.tar", "name", ".", "path", "member.tar.d/none", "reason", "invalid")))
	})

	It("copies members larger than one read, byte for byte", func() {
		big := strings.Repeat("0123456789abcdef", 64*1024)
		f := newFixture(archives.Zip(
			archives.Entry{Name: "big.log", Body: big},
			archives.Entry{Name: "big.tar", Body: string(archives.Tar(archives.Entry{Name: "big.log", Body: big}))},
		))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(HaveKeyWithValue("big.log", big), HaveKeyWithValue("big.tar.d/big.log", big)))
	})

	It("keeps a nested archive that does not read as a file, and records why", func() {
		f := newFixture(archives.Zip(archives.Entry{Name: "broken.zip", Body: "PK\x03\x04 truncated"}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(HaveKeyWithValue("broken.zip", "PK\x03\x04 truncated"))
		Expect(f.manifest()["not_expanded"]).To(ConsistOf(record("archive", "artifact.zip", "name", "broken.zip", "path", "broken.zip", "reason", "zip: not a valid zip file")))
	})

	It("skips, and records, a zip member that fails to read, keeping the members after it", func() {
		members := []archives.Entry{
			{Name: "a.log", Body: "a\n"},
			{Name: "crc.log", Body: strings.Repeat("c", 100), BadCRC: true},
			{Name: "bzip2.log", Body: "b\n", Method: 12},
			{Name: "d.log", Body: "d\n"},
		}
		f := newFixture(archives.Zip(append(members, archives.Entry{Name: "inner.zip", Body: string(archives.Zip(members...))})...))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(
			HaveKeyWithValue("a.log", "a\n"), HaveKeyWithValue("d.log", "d\n"),
			HaveKeyWithValue("inner.zip.d/a.log", "a\n"), HaveKeyWithValue("inner.zip.d/d.log", "d\n"),
		))
		Expect(f.files()).To(HaveLen(6))
		Expect(f.manifest()["skipped"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", "crc.log", "reason", "corrupt: zip: checksum error"),
			record("archive", "artifact.zip", "name", "bzip2.log", "reason", "corrupt: zip: unsupported compression algorithm"),
			record("archive", "inner.zip", "name", "crc.log", "reason", "corrupt: zip: checksum error"),
			record("archive", "inner.zip", "name", "bzip2.log", "reason", "corrupt: zip: unsupported compression algorithm"),
		))
		Expect(f.manifest()["not_expanded"]).To(BeEmpty())
	})

	It("keeps what a nested tar.gz holds before it is cut short, skips the member it cuts, and records the archive as partial", func() {
		tgz := archives.TarGz(
			archives.Entry{Name: "first.log", Body: "first\n"},
			archives.Entry{Name: "second.log", Body: strings.Repeat("0123456789abcdef", 64*1024)},
		)
		f := newFixture(archives.Zip(archives.Entry{Name: "logs.tgz", Body: string(tgz[:len(tgz)*2/3])}))

		Expect(f.extract(extract.Defaults())).To(Succeed())
		Expect(f.files()).To(SatisfyAll(HaveKeyWithValue("logs.tgz.d/first.log", "first\n"), HaveLen(3)))
		Expect(f.manifest()["skipped"]).To(ConsistOf(record("archive", "logs.tgz", "name", "second.log", "reason", "corrupt: unexpected EOF")))
		Expect(f.manifest()["not_expanded"]).To(ConsistOf(record("archive", "artifact.zip", "name", "logs.tgz", "path", "logs.tgz", "reason", "partial: tar.gz: unexpected EOF")))
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

	It("counts toward MaxBytes the bytes of members that fail to read", func() {
		bad := archives.Entry{Name: "bad.log", Body: strings.Repeat("x", 1000), BadCRC: true}
		var nested []archives.Entry
		for i := range 3 {
			nested = append(nested, archives.Entry{Name: fmt.Sprintf("n%d.zip", i), Body: string(archives.Zip(bad))})
		}
		f := newFixture(archives.Zip(nested...))
		limits := extract.Defaults()
		limits.MaxBytes = 3 * int64(len(nested[0].Body)+999)

		Expect(f.extract(limits)).To(MatchError(store.ErrTooLarge))
		Expect(f.extracted).NotTo(BeADirectory())
		Expect(filepath.Join(f.root, "tmp")).To(matchers.BeSwept())
	})

	It("fails before writing anything on an artifact.zip of more than MaxFiles members, skipped ones included", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "dir/", Mode: fs.ModeDir | 0o755},
			archives.Entry{Name: "dir/a.log"},
			archives.Entry{Name: "link", Mode: fs.ModeSymlink | 0o777, Link: "a.log"},
			archives.Entry{Name: "b.log"},
		))
		limits := extract.Defaults()
		Expect(limits.MaxFiles).To(Equal(100_000))
		limits.MaxFiles = 2

		Expect(f.extract(limits)).To(MatchError(extract.ErrTooManyFiles))
		Expect(f.fsys.Journal()).NotTo(ContainElement(HaveField("Name", "create")))
		Expect(f.extracted).NotTo(BeADirectory())
		Expect(filepath.Join(f.root, "tmp")).To(matchers.BeSwept())

		limits.MaxFiles++
		Expect(f.extract(limits)).To(Succeed())
	})

	It("expands nested archives only while the members met, skipped ones included, stay within MaxFiles, records the rest as not expanded, and reserves no <archive>.d for them", func() {
		f := newFixture(archives.Zip(
			archives.Entry{Name: "a.log", Body: "a\n"},
			archives.Entry{Name: "b.tar", Body: string(archives.Tar(
				archives.Entry{Name: "b1.log", Body: "b1\n"},
				archives.Entry{Name: "link", TarType: tar.TypeSymlink, Link: "b1.log"},
				archives.Entry{Name: "b2.log", Body: "b2\n"},
			))},
			archives.Entry{Name: "c.zip", Body: string(archives.Zip(archives.Entry{Name: "c.log", Body: "c\n"}))},
			archives.Entry{Name: "c.zip.d/x.log", Body: "x\n"},
			archives.Entry{Name: "d.log", Body: "d\n"},
		))
		limits := extract.Defaults()
		limits.MaxFiles = 7

		Expect(f.extract(limits)).To(Succeed())
		Expect(f.files()).To(SatisfyAll(
			HaveLen(7), HaveKey("a.log"), HaveKey("b.tar"), HaveKeyWithValue("b.tar.d/b1.log", "b1\n"), HaveKey("c.zip"), HaveKey("c.zip.d/x.log"), HaveKey("d.log"),
		))
		Expect(f.manifest()["renamed"]).To(BeEmpty())
		Expect(f.manifest()["not_expanded"]).To(ConsistOf(
			record("archive", "artifact.zip", "name", "b.tar", "path", "b.tar", "reason", "partial: too_many_files"),
			record("archive", "artifact.zip", "name", "c.zip", "path", "c.zip", "reason", "too_many_files"),
		))
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
