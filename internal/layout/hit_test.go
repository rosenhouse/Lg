package layout_test

import (
	"slices"

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
	Entry("a path", "/lg:home/log.txt", []string{"/lg:home/log.txt"},
		layout.Hit{Path: "/lg:home/log.txt"}, true),
	Entry("a path and an empty text", "/lg:home/log.txt:7:", []string{"/lg:home/log.txt"},
		layout.Hit{Path: "/lg:home/log.txt", Line: 7}, true),
	Entry("two existing prefixes", "a:b:12:x", []string{"a", "a:b"},
		layout.Hit{Path: "a:b", Line: 12, Text: "x"}, true),
	Entry("a line that is not a number", "a:12x:y", []string{"a"},
		layout.Hit{Path: "a", Text: "12x:y"}, true),
	Entry("no existing prefix", "a:1:b", []string{"b"},
		layout.Hit{}, false),
)
