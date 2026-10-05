package status_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/status"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

var _ = Describe("Read", Label("status"), func() {
	It("reports absence as nil when there is no file", func() {
		st, err := status.Read(filepath.Join(GinkgoT().TempDir(), "status.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(st).To(BeNil())
	})

	It("names the file it cannot parse", func() {
		path := filepath.Join(GinkgoT().TempDir(), "status.json")
		Expect(os.WriteFile(path, []byte("{"), 0o644)).To(Succeed())

		_, err := status.Read(path)
		Expect(err).To(MatchError(HavePrefix(path + ": ")))
	})
})

var _ = Describe("Write", Label("status"), func() {
	var path string

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "status.json")
	})

	It("writes indented JSON with a trailing newline that Read gives back", func() {
		ok := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
		st := status.Status{LgFormat: 1, Cycle: 2, LastSyncOKAt: &ok, Repos: map[string]status.Repo{"github.com/o/r": {Runs: 3}}}

		Expect(status.Write(store.OSFS{}, path, st)).To(Succeed())

		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(HavePrefix("{\n  \"lg_format\": 1,\n"))
		Expect(string(raw)).To(HaveSuffix("}\n"))
		Expect(status.Read(path)).To(Equal(&st))
	})

	It("writes a file that other accounts can read", func() {
		Expect(status.Write(store.OSFS{}, path, status.Status{LgFormat: 1})).To(Succeed())

		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o644)))
	})

	It("writes <, > and & as they are", func() {
		st := status.Status{Repos: map[string]status.Repo{"github.com/o/r": {Pending: []status.Pending{{Unit: status.Unit{Run: 1}, Error: "runs?created=a..b&per_page=100: <html>"}}}}}

		Expect(status.Write(store.OSFS{}, path, st)).To(Succeed())
		Expect(os.ReadFile(path)).To(ContainSubstring(`"error": "runs?created=a..b&per_page=100: <html>"`))
	})

	It("writes through fsys", func() {
		fsys := faultfs.New()
		fsys.FailOn("rename", syscall.ENOSPC)

		Expect(status.Write(fsys, path, status.Status{LgFormat: 1})).To(MatchError(syscall.ENOSPC))
		Expect(path).NotTo(BeAnExistingFile())
	})

	DescribeTable("rewrites the file in place when the disk is too full for a temp file",
		func(errno syscall.Errno) {
			Expect(os.WriteFile(path, []byte(`{"cycle": 1}`+strings.Repeat(" ", 100)), 0o644)).To(Succeed())
			fsys := faultfs.New()
			fsys.FailOn("create", errno)

			Expect(status.Write(fsys, path, status.Status{LgFormat: 1, Cycle: 2})).To(Succeed())

			Expect(status.Read(path)).To(HaveField("Cycle", int64(2)))
			Expect(os.ReadFile(path)).To(HaveSuffix("}\n"))
			Expect(path + ".tmp").NotTo(BeAnExistingFile())
		},
		Entry("ENOSPC", syscall.ENOSPC),
		Entry("EDQUOT", syscall.EDQUOT),
	)

	It("keeps the file after any other failure", func() {
		Expect(os.WriteFile(path, []byte("old\n"), 0o644)).To(Succeed())
		fsys := faultfs.New()
		fsys.FailOn("create", syscall.EIO)

		Expect(status.Write(fsys, path, status.Status{LgFormat: 1})).To(MatchError(syscall.EIO))
		Expect(os.ReadFile(path)).To(Equal([]byte("old\n")))
	})

	It("replaces the file by renaming a temp file over it, leaving no temp file", func() {
		Expect(os.WriteFile(path, []byte("old\n"), 0o644)).To(Succeed())
		link := path + ".link"
		Expect(os.Link(path, link)).To(Succeed())

		Expect(status.Write(store.OSFS{}, path, status.Status{LgFormat: 1})).To(Succeed())

		Expect(os.ReadFile(path)).To(HavePrefix("{"))
		Expect(os.ReadFile(link)).To(Equal([]byte("old\n")))
		Expect(os.ReadDir(filepath.Dir(path))).To(ConsistOf(HaveField("Name()", "status.json"), HaveField("Name()", "status.json.link")))
	})
})
