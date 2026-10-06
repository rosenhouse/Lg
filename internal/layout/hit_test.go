package layout_test

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/layout"
)

var _ = DescribeTable("ParseHit splits an rg or grep hit at the longest prefix that exists", Label("where"),
	func(hit string, existing []string, want layout.Hit, ok bool) {
		got, gotOK := layout.ParseHit(hit, func(path string) bool { return slices.Contains(existing, path) })
		Expect(gotOK).To(Equal(ok))
		Expect(got).To(Equal(want))
	},
	Entry("path:line:text", "/lg:home/log.txt:123:text: with colons", []string{"/lg:home/log.txt"},
		layout.Hit{Path: "/lg:home/log.txt", Line: 123, Text: "text: with colons"}, true),
	Entry("path:text", "/lg:home/log.txt:text: 12: with colons", []string{"/lg:home/log.txt"},
		layout.Hit{Path: "/lg:home/log.txt", Text: "text: 12: with colons"}, true),
	Entry("path:line", "/lg:home/log.txt:2231", []string{"/lg:home/log.txt"},
		layout.Hit{Path: "/lg:home/log.txt", Line: 2231}, true),
	Entry("a context line", "/lg-home/log.txt-12-text-with: dashes", []string{"/lg-home/log.txt"},
		layout.Hit{Path: "/lg-home/log.txt", Line: 12, Text: "text-with: dashes"}, true),
	Entry("a context line with no text", "a-b-12-", []string{"a", "a-b"},
		layout.Hit{Path: "a-b", Line: 12}, true),
	Entry("a dash with no line after it", "a-b-x", []string{"a"},
		layout.Hit{}, false),
	Entry("a dash and a line with nothing after it", "a-12", []string{"a"},
		layout.Hit{}, false),
	Entry("a path", "/lg:home/log.txt", []string{"/lg:home/log.txt"},
		layout.Hit{Path: "/lg:home/log.txt"}, true),
	Entry("a path and an empty text", "/lg:home/log.txt:7:", []string{"/lg:home/log.txt"},
		layout.Hit{Path: "/lg:home/log.txt", Line: 7}, true),
	Entry("two existing prefixes", "a:b:12:x", []string{"a", "a:b"},
		layout.Hit{Path: "a:b", Line: 12, Text: "x"}, true),
	Entry("a line that is not a number", "a:12x:y", []string{"a"},
		layout.Hit{Path: "a", Text: "12x:y"}, true),
	Entry("a line with a leading zero", "a:012:x", []string{"a"},
		layout.Hit{Path: "a", Text: "012:x"}, true),
	Entry("a line with a sign", "a:+12", []string{"a"},
		layout.Hit{Path: "a", Text: "+12"}, true),
	Entry("a line 0, which is no line", "a:0:x", []string{"a"},
		layout.Hit{Path: "a", Text: "0:x"}, true),
	Entry("an empty path", ":1:x", []string{""},
		layout.Hit{}, false),
	Entry("no existing prefix", "a:1:b", []string{"b"},
		layout.Hit{}, false),
	Entry("a path as long as Linux allows", strings.Repeat("a", 4095)+":1:x", []string{strings.Repeat("a", 4095)},
		layout.Hit{Path: strings.Repeat("a", 4095), Line: 1, Text: "x"}, true),
)

var _ = It("ParseHit tries no prefix longer than a path can be", Label("where"), func() {
	text := strings.Repeat("a:", 1<<19)
	var longest int
	hit, ok := layout.ParseHit("path:1:"+text, func(path string) bool {
		longest = max(longest, len(path))
		return path == "path"
	})
	Expect(ok).To(BeTrue())
	Expect(hit).To(Equal(layout.Hit{Path: "path", Line: 1, Text: text}))
	Expect(longest).To(BeNumerically("<=", 4095))
})
