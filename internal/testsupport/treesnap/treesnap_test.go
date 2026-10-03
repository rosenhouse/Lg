package treesnap_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

var _ = Describe("BeAppendOnlyFrom", Label("store"), func() {
	var (
		root   string
		before treesnap.Snap
	)

	write := func(name, content string) {
		path := filepath.Join(root, name)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
	}

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		write("a/log.txt", "log")
		before = treesnap.Snapshot(root)
	})

	It("accepts new files and dirs, even though they change their parent dir's mtime", func() {
		write("a/b/new.txt", "new")
		Expect(treesnap.Snapshot(root)).To(treesnap.BeAppendOnlyFrom(before))
	})

	DescribeTable("rejects any change to an existing file or dir",
		func(change func(path string)) {
			change(filepath.Join(root, "a/log.txt"))
			Expect(treesnap.Snapshot(root)).NotTo(treesnap.BeAppendOnlyFrom(before))
		},
		Entry("same-size content", func(path string) { Expect(os.WriteFile(path, []byte("LOG"), 0o644)).To(Succeed()) }),
		Entry("size", func(path string) { Expect(os.WriteFile(path, []byte("log2"), 0o644)).To(Succeed()) }),
		Entry("mode", func(path string) { Expect(os.Chmod(path, 0o600)).To(Succeed()) }),
		Entry("mtime", func(path string) {
			info, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Chtimes(path, info.ModTime(), info.ModTime().Add(time.Second))).To(Succeed())
		}),
		Entry("removal", func(path string) { Expect(os.Remove(path)).To(Succeed()) }),
		Entry("dir mode", func(path string) { Expect(os.Chmod(filepath.Dir(path), 0o700)).To(Succeed()) }),
		Entry("dir removal", func(path string) { Expect(os.RemoveAll(filepath.Dir(path))).To(Succeed()) }),
	)

	It("names the changed path in its failure message", func() {
		write("a/log.txt", "LOG")
		matcher := treesnap.BeAppendOnlyFrom(before)
		Expect(matcher.Match(treesnap.Snapshot(root))).To(BeFalse())
		Expect(matcher.FailureMessage(treesnap.Snapshot(root))).To(ContainSubstring("a/log.txt"))
	})

	It("snapshots a missing dir as empty", func() {
		Expect(treesnap.Snapshot(filepath.Join(root, "missing"))).To(BeEmpty())
	})
})
