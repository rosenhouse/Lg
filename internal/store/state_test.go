package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/store"
)

var _ = Describe("state files", Label("store"), func() {
	var s *store.Store

	BeforeEach(func() {
		s = open(newStore())
	})

	It("writes compact JSON with <, > and & as they are, and reads it back", func() {
		Expect(s.WriteState("x.json", map[string]string{"a": "<&>"})).To(Succeed())
		Expect(os.ReadFile(filepath.Join(s.State(), "x.json"))).To(Equal([]byte(`{"a":"<&>"}` + "\n")))

		var got map[string]string
		discarded, err := s.ReadState("x.json", func(raw []byte) error { return json.Unmarshal(raw, &got) })
		Expect(err).NotTo(HaveOccurred())
		Expect(discarded).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string]string{"a": "<&>"}))
	})

	It("decodes nothing from a missing file", func() {
		discarded, err := s.ReadState("x.json", func([]byte) error {
			Fail("decoded a missing file")
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(discarded).NotTo(HaveOccurred())
	})

	It("moves a file that decode rejects aside, and gives decode's error as discarded", func() {
		path := filepath.Join(s.State(), "x.json")
		Expect(os.WriteFile(path, []byte("{"), 0o644)).To(Succeed())

		discarded, err := s.ReadState("x.json", func(raw []byte) error { return json.Unmarshal(raw, new(any)) })
		Expect(err).NotTo(HaveOccurred())
		var corrupt *store.CorruptFileError
		Expect(discarded).To(BeAssignableToTypeOf(corrupt))
		Expect(discarded).To(MatchError(ContainSubstring(path)))
		Expect(discarded).To(MatchError(ContainSubstring("moved to " + path + ".corrupt")))
		Expect(path).NotTo(BeAnExistingFile())
		Expect(os.ReadFile(path + ".corrupt")).To(Equal([]byte("{")))
	})
})
