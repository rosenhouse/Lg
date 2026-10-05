package store_test

import (
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

var _ = Describe("Unit", Label("sync"), func() {
	var (
		root   string
		s      *store.Store
		unit   *store.Unit
		target string
	)

	BeforeEach(func() {
		root = newStore()
		s = open(root)
		var err error
		unit, err = s.NewUnit()
		Expect(err).NotTo(HaveOccurred())
		target = filepath.Join(root, "data", "runs", "attempt-1")
	})

	It("writes JSON indented two spaces, keeping key order, number literals and string escapes, and ending in a newline", func() {
		Expect(unit.WriteJSON("a.json", []byte(`{"z":1.50,"a":[1e3,{}],"s":"\u00e9\/<"}`))).To(Succeed())
		Expect(s.Publish(unit, target)).To(Succeed())

		Expect(os.ReadFile(filepath.Join(target, "a.json"))).To(Equal([]byte(
			"{\n  \"z\": 1.50,\n  \"a\": [\n    1e3,\n    {}\n  ],\n  \"s\": \"\\u00e9\\/<\"\n}\n")))
	})

	It("publishes members created in subdirs by renaming the staged unit into place", func() {
		w, err := unit.Create("jobs/1_build/log.txt", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte("\xef\xbb\xbflog"))
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())
		Expect(s.Publish(unit, target)).To(Succeed())

		Expect(os.ReadFile(filepath.Join(target, "jobs/1_build/log.txt"))).To(Equal([]byte("\xef\xbb\xbflog")))
		Expect(filepath.Join(root, "tmp")).To(matchers.BeSwept())
	})

	It("publishes the unit dir with the mode MkdirAll gives its siblings", func() {
		Expect(s.Publish(unit, target)).To(Succeed())
		sibling := filepath.Join(filepath.Dir(target), "sibling")
		Expect(os.MkdirAll(sibling, 0o755)).To(Succeed())

		want, err := os.Stat(sibling)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Stat(target)).To(HaveField("Mode()", want.Mode()))
	})

	DescribeTable("rejects a member name that is absolute or contains ..", Label("store"),
		func(name string) {
			_, err := unit.Create(name, store.Unlimited)
			Expect(err).To(MatchError(ContainSubstring(name)))
			Expect(unit.WriteJSON(name, []byte("{}"))).To(MatchError(ContainSubstring(name)))
			Expect(filepath.Join(root, "escaped.json")).NotTo(BeAnExistingFile())
		},
		Entry("absolute", "/escaped.json"),
		Entry("leading ..", "../../escaped.json"),
		Entry(".. inside", "jobs/../../../escaped.json"),
		Entry(".. that stays inside", "jobs/../a.json"),
	)

	It("Abort removes the staged unit", Label("store"), func() {
		Expect(unit.WriteJSON("a.json", []byte("{}"))).To(Succeed())
		Expect(unit.Abort()).To(Succeed())
		Expect(filepath.Join(root, "tmp")).To(matchers.BeSwept())
	})

	It("fails and publishes nothing when its staging dir vanishes", Label("store"), func() {
		Expect(unit.WriteJSON("attempt.json", []byte("{}"))).To(Succeed())
		Expect(os.RemoveAll(filepath.Join(root, "tmp"))).To(Succeed())
		before := treesnap.Snapshot(filepath.Join(root, "data"))

		Expect(unit.WriteJSON("jobs/1_build/job.json", []byte("{}"))).To(MatchError(fs.ErrNotExist))
		Expect(s.Publish(unit, target)).To(MatchError(fs.ErrNotExist))
		Expect(treesnap.Snapshot(filepath.Join(root, "data"))).To(Equal(before))
		Expect(filepath.Join(root, "tmp")).NotTo(BeAnExistingFile())
	})

	It("refuses to create a member twice", func() {
		w, err := unit.Create("log.txt", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())

		_, err = unit.Create("log.txt", store.Unlimited)
		Expect(err).To(MatchError(os.ErrExist))
	})

	It("gives the bytes and sha256 of each closed member", Label("failures"), func() {
		w, err := unit.Create("jobs/1_build/log.txt", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte("ab"))
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte("c"))
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())
		Expect(unit.WriteJSON("a.json", []byte(`{}`))).To(Succeed())

		Expect(unit.Sum("jobs/1_build/log.txt")).To(Equal(store.Sum{Bytes: 3, SHA256: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}))
		Expect(unit.Sum("a.json")).To(Equal(store.Sum{Bytes: 3, SHA256: "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356"}))
	})

	It("fails a write past maxBytes with ErrTooLarge, keeping the bytes and sha256 of what fit", Label("artifacts"), func() {
		w, err := unit.Create("artifact.zip", 3)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte("ab"))
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte("c"))
		Expect(err).NotTo(HaveOccurred())

		n, err := w.Write([]byte("d"))
		Expect(err).To(MatchError(store.ErrTooLarge))
		Expect(err).To(MatchError(ContainSubstring(`"artifact.zip" exceeds 3 bytes`)))
		Expect(n).To(BeZero())
		Expect(w.Close()).To(Succeed())
		Expect(unit.Sum("artifact.zip")).To(Equal(store.Sum{Bytes: 3, SHA256: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}))
	})

	It("refuses the sum of a member not closed or never created", Label("failures"), func() {
		w, err := unit.Create("log.txt", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(w.Close)

		_, err = unit.Sum("log.txt")
		Expect(err).To(MatchError(`member "log.txt" is not closed`))
		_, err = unit.Sum("other.txt")
		Expect(err).To(MatchError(`member "other.txt" is not closed`))
	})

	It("Remove deletes a closed member, so Publish leaves it out", Label("failures"), func() {
		w, err := unit.Create("jobs/1_build/log.txt", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())
		Expect(unit.WriteJSON("jobs/1_build/log.txt.tombstone", []byte(`{}`))).To(Succeed())

		Expect(unit.Remove("jobs/1_build/log.txt")).To(Succeed())
		_, err = unit.Sum("jobs/1_build/log.txt")
		Expect(err).To(HaveOccurred())
		Expect(s.Publish(unit, target)).To(Succeed())
		Expect(os.ReadDir(filepath.Join(target, "jobs/1_build"))).To(HaveExactElements(HaveField("Name()", "log.txt.tombstone")))
	})

	It("Remove refuses a member still open", Label("failures"), func() {
		w, err := unit.Create("log.txt", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(w.Close)

		Expect(unit.Remove("log.txt")).To(MatchError(`member "log.txt" is not closed`))
	})
})
