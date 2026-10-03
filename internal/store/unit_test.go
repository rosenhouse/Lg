package store_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/store"
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
		w, err := unit.Create("jobs/1_build/log.txt")
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte("\xef\xbb\xbflog"))
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())
		Expect(s.Publish(unit, target)).To(Succeed())

		Expect(os.ReadFile(filepath.Join(target, "jobs/1_build/log.txt"))).To(Equal([]byte("\xef\xbb\xbflog")))
		Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty())
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
			_, err := unit.Create(name)
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
		Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty())
	})

	It("refuses to create a member twice", func() {
		w, err := unit.Create("log.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())

		_, err = unit.Create("log.txt")
		Expect(err).To(MatchError(os.ErrExist))
	})
})
