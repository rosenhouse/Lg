package grep_test

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing/iotest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/grep"
)

// hits gives each line of content that pattern matches, as "n:text".
func hits(pattern, content string) []string {
	GinkgoHelper()
	m, err := grep.Compile(pattern)
	Expect(err).NotTo(HaveOccurred())
	got := []string{}
	Expect(m.Lines(strings.NewReader(content), func(n int, text []byte) bool {
		got = append(got, fmt.Sprintf("%d:%s", n, text))
		return true
	})).To(Succeed())
	return got
}

var _ = DescribeTable("Lines matches the pattern in each line on its own, as rg does, with", Label("grep"),
	func(pattern, content string, want ...string) {
		Expect(hits(pattern, content)).To(HaveExactElements(want))
	},
	Entry("^ and $ at each line's ends", `^foo$`, "x\nfoo\nfoo bar\n", "2:foo"),
	Entry(`\A and \z at each line's ends`, `\Afoo\z`, "x\nfoo\nfoo bar\n", "2:foo"),
	Entry(`\b at each line's ends`, `\bfoo\b`, "xfoo\nfoo\n", "2:foo"),
	Entry(`\s matching no newline`, `foo\sbar`, "foo\nbar\nfoo\tbar\n", "3:foo\tbar"),
	Entry("a negated class matching no newline", `a[^x]b`, "a\nb\na b\n", "3:a b"),
	Entry("(?s). matching no newline", `(?s)a.b`, "a\nb\na-b\n", "3:a-b"),
	Entry("an escaped newline matching nothing", `a\nb|c`, "a\nb\nc\n", "3:c"),
	Entry("an empty pattern matching every line", ``, "a\n\nb", "1:a", "2:", "3:b"),
	Entry("^$ matching an empty line, but nothing after the last newline", `^$`, "a\n\nb\n", "2:"),
	Entry("a literal that every match holds, also in lines that do not match", `\d+ errors`, "no errors\n3 errors\n", "2:3 errors"),
	Entry("a case-insensitive literal", `(?i)ERROR`, "an Error\nfine\nERRORS\n", "1:an Error", "3:ERRORS"),
	Entry("an alternation of literals", `foo|bar`, "bar\nfoo\nx\nbar foo\nfoo\n", "1:bar", "2:foo", "4:bar foo", "5:foo"),
	Entry("an alternation of case-insensitive literals", `(?i)error|warning`, "an Error\nfine\nWARNING x\n", "1:an Error", "3:WARNING x"),
	Entry("long s and the Kelvin sign matching s and k case-insensitively", `(?i)ks`, "\u212a\u017f\nks\nKS\nkx\n", "1:\u212a\u017f", "2:ks", "3:KS"),
	Entry("a case-insensitive literal that is not ASCII", `(?i)caf\x{E9}`, "CAF\u00c9\n", "1:CAF\u00c9"),
	Entry("U+FFFD matching invalid UTF-8", `\x{FFFD}`, "a\xffb\n", "1:a\xffb"),
)

var _ = DescribeTable("Compile finds literals that every match holds one of, for", Label("grep"),
	func(pattern string, fold bool, literals ...string) {
		gotLiterals, gotFold := grep.RequiredLiterals(pattern)
		Expect(gotLiterals).To(HaveExactElements(literals))
		Expect(gotFold).To(Equal(fold))
	},
	Entry("a literal", `foo`, false, "foo"),
	Entry("the longest literal of a concatenation", `\d+ errors \d`, false, " errors "),
	Entry("a literal in a group", `\d(errors)`, false, "errors"),
	Entry("a case-insensitive literal, lowered", `(?i)ERROR`, true, "error"),
	Entry("an alternation of literals", `foo|bar`, false, "foo", "bar"),
	Entry("the set whose shortest literal is longest", `(?i)(error|warning): `, true, "error", "warning"),
	Entry("a literal longer than a set's shortest", `(a|bcdef)\dxyz`, false, "xyz"),
	Entry("none in an alternation with a branch without one", `foo|\d`, false),
	Entry("none in literals that differ in case sensitivity", `(?i:foo)|bar`, false),
	Entry("none in a case-insensitive literal that is not ASCII", `(?i)caf\x{E9}`, false),
	Entry("none in a literal holding U+FFFD", `a\x{FFFD}`, false),
)

var _ = Describe("Lines", Label("grep"), func() {
	It("strips a leading UTF-8 BOM, so ^ matches at line 1's start", func() {
		Expect(hits(`^\S+ first`, "\ufeff2026-10-03T14:22:58Z first\n2026-10-03T14:22:59Z second\n")).
			To(HaveExactElements("1:2026-10-03T14:22:58Z first"))
	})

	It("reads no line of a binary file: one with a NUL byte in its first 8 KiB", func() {
		Expect(hits("needle", strings.Repeat("x", 8191)+"\x00\nneedle\n")).To(BeEmpty())
		Expect(hits("needle", strings.Repeat("x", 8192)+"\x00\nneedle\n")).To(HaveExactElements("2:needle"))
	})

	It("finds a hit on a line longer than a read, and on a last line without a newline", func() {
		long := strings.Repeat("y", 1<<20) + " needle"
		Expect(hits("needle", "short\n"+long+"\nneedle at the end")).To(HaveExactElements("2:"+long, "3:needle at the end"))
	})

	It("numbers lines across reads, and finds a line that a read splits", func() {
		var content strings.Builder
		var want []string
		split := 0
		for n := 1; n <= 100000; n++ {
			line := fmt.Sprintf("line %d", n)
			if n%25000 == 0 || split == 0 && content.Len()+len(line+" needle") > 256<<10 {
				line += " needle"
				want = append(want, fmt.Sprintf("%d:%s", n, line))
				if n%25000 != 0 {
					split = n
				}
			}
			content.WriteString(line + "\n")
		}
		Expect(split).To(BeNumerically(">", 0))

		Expect(hits("needle", content.String())).To(HaveExactElements(want))
	})

	It("stops when hit returns false", func() {
		m, err := grep.Compile("x")
		Expect(err).NotTo(HaveOccurred())
		calls := 0
		Expect(m.Lines(strings.NewReader("x\nx\n"), func(int, []byte) bool {
			calls++
			return false
		})).To(Succeed())
		Expect(calls).To(Equal(1))
	})

	It("gives the error of a read", func() {
		m, err := grep.Compile("x")
		Expect(err).NotTo(HaveOccurred())
		failing := io.MultiReader(strings.NewReader("x\n"), iotest.ErrReader(errors.New("input/output error")))
		Expect(m.Lines(failing, func(int, []byte) bool { return true })).To(MatchError("input/output error"))
	})
})

var _ = Describe("Compile", Label("grep"), func() {
	It("refuses an invalid pattern", func() {
		_, err := grep.Compile("foo(")
		Expect(err).To(MatchError(ContainSubstring("missing closing )")))
	})
})
