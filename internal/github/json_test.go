package github_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
)

var _ = Describe("Run and Artifact as JSON", Label("artifacts"), func() {
	It("decode into their fields and Raw, and encode as Raw", func() {
		raw := `{"id":7,"name":"a&<b>","extra":[1]}`
		var artifact github.Artifact
		Expect(json.Unmarshal([]byte(raw), &artifact)).To(Succeed())
		Expect(artifact.ID).To(Equal(int64(7)))
		Expect(string(artifact.Raw)).To(Equal(raw))

		var run github.Run
		Expect(json.Unmarshal([]byte(raw), &run)).To(Succeed())
		Expect(run.Name).To(Equal("a&<b>"))
		Expect(string(run.Raw)).To(Equal(raw))

		Expect(json.Marshal(artifact)).To(MatchJSON(raw))
		Expect(json.Marshal(run)).To(MatchJSON(raw))
	})
})
