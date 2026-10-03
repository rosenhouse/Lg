package layout_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/layout"
)

var _ = Describe("Slug", Label("sync"), func() {
	DescribeTable("keeps [A-Za-z0-9.-], turns other bytes into one -, and trims - and . from the ends",
		func(name, slug string) {
			Expect(layout.Slug(name)).To(Equal(slug))
		},
		Entry(nil, "build (ubuntu-latest, 1.22)", "build-ubuntu-latest-1.22"),
		Entry(nil, "feat/retry upload", "feat-retry-upload"),
		Entry(nil, "Release/3.x", "Release-3.x"),
		Entry(nil, "", "none"),
		Entry(nil, "--a__b..", "a-b"),
		Entry(nil, "日本", "none"),
	)

	It("truncates to 60 bytes and drops a - that truncation leaves at the end", func() {
		name := strings.Repeat("a", 59) + "-bcd"
		Expect(layout.Slug(name)).To(Equal(strings.Repeat("a", 59)))
	})

	It("turns the recorded unicode job name into 60 bytes", func() {
		slug := layout.Slug("ünïcode / slash: job with a very long name that keeps going well past sixty characters")
		Expect(slug).To(Equal("n-code-slash-job-with-a-very-long-name-that-keeps-going-well"))
		Expect(slug).To(HaveLen(60))
	})
})
