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
		root = GinkgoT().TempDir()
		s = store.New(filepath.Join(root, "tmp"))
		var err error
		unit, err = s.NewUnit()
		Expect(err).NotTo(HaveOccurred())
		target = filepath.Join(root, "data", "runs", "attempt-1")
	})

	It("writes JSON indented two spaces, keeping key order, number literals and string escapes, and ending in a newline", func() {
		Expect(unit.WriteJSON("a.json", []byte(`{"z":1.50,"a":[1e3,{}],"s":"é\/<"}`))).To(Succeed())
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
})
