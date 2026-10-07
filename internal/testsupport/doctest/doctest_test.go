package doctest_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/doctest"
)

var _ = Describe("ShBlocks", Label("skill"), func() {
	It("finds sh blocks and ignores the others", func() {
		md := "# Title\n" +
			"```sh\nlg status\nlg sync --wait\n```\n" +
			"text\n" +
			"```json\n{\"sh\": 1}\n```\n" +
			"```\nuntagged\n```\n" +
			"```shell\nnot sh\n```\n" +
			"  ```sh\nindented\n  ```\n"
		Expect(doctest.ShBlocks(md)).To(Equal([]doctest.Block{
			{Line: 2, Text: "lg status\nlg sync --wait\n"},
			{Line: 16, Text: "indented\n"},
		}))
	})
})
