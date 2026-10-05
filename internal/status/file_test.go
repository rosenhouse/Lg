package status_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/status"
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

		Expect(status.Write(path, st)).To(Succeed())

		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(HavePrefix("{\n  \"lg_format\": 1,\n"))
		Expect(string(raw)).To(HaveSuffix("}\n"))
		Expect(status.Read(path)).To(Equal(&st))
	})

	It("replaces the file by renaming a temp file over it, leaving no temp file", func() {
		Expect(os.WriteFile(path, []byte("old\n"), 0o644)).To(Succeed())
		link := path + ".link"
		Expect(os.Link(path, link)).To(Succeed())

		Expect(status.Write(path, status.Status{LgFormat: 1})).To(Succeed())

		Expect(os.ReadFile(path)).To(HavePrefix("{"))
		Expect(os.ReadFile(link)).To(Equal([]byte("old\n")))
		Expect(os.ReadDir(filepath.Dir(path))).To(ConsistOf(HaveField("Name()", "status.json"), HaveField("Name()", "status.json.link")))
	})
})
