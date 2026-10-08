package grep_test

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing/iotest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/grep"
)

// hits gives each line of content that pattern matches, as "n:text".
func hits(pattern, content string, flags ...grep.Flag) []string {
	GinkgoHelper()
	m, err := grep.Compile(pattern, flags...)
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
	Entry("a class holding a newline", `x[a\n]`, "x\na\nxa\n", "3:xa"),
	Entry("an empty pattern matching every line", ``, "a\n\nb", "1:a", "2:", "3:b"),
	Entry("^$ matching an empty line, but nothing after the last newline", `^$`, "a\n\nb\n", "2:"),
	Entry("a literal that every match holds, also in lines that do not match", `\d+ errors`, "no errors\n3 errors\n", "2:3 errors"),
	Entry("a case-insensitive literal", `(?i)ERROR`, "an Error\nfine\nERRORS\n", "1:an Error", "3:ERRORS"),
	Entry("an alternation of literals", `foo|bar`, "bar\nfoo\nx\nbar foo\nfoo\n", "1:bar", "2:foo", "4:bar foo", "5:foo"),
	Entry("an alternation of case-insensitive literals", `(?i)error|warning`, "an Error\nfine\nWARNING x\n", "1:an Error", "3:WARNING x"),
	Entry("long s matching s case-insensitively", `(?i)s`, "\u017f\nx\nS\n", "1:\u017f", "3:S"),
	Entry("the Kelvin sign matching k case-insensitively", `(?i)k`, "\u212a\nx\nK\n", "1:\u212a", "3:K"),
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

	It("gives the error of a read, after the hits in the lines read before it", func() {
		m, err := grep.Compile("x")
		Expect(err).NotTo(HaveOccurred())
		failing := io.MultiReader(strings.NewReader("x 1\nx 2"), iotest.ErrReader(errors.New("input/output error")))
		var got []string
		Expect(m.Lines(failing, func(n int, text []byte) bool {
			got = append(got, fmt.Sprintf("%d:%s", n, text))
			return true
		})).To(MatchError("input/output error"))
		Expect(got).To(HaveExactElements("1:x 1"))
	})

	It("gives the error of a read before a newline, or in a binary file", func() {
		m, err := grep.Compile("abc")
		Expect(err).NotTo(HaveOccurred())
		for _, content := range []string{"abc", "abc\x00\n"} {
			failing := io.MultiReader(strings.NewReader(content), &failOnce{err: errors.New("input/output error")})
			Expect(m.Lines(failing, func(int, []byte) bool { return true })).To(MatchError("input/output error"), "%q", content)
		}
	})

	DescribeTable("takes linear time for a pattern that could match from one line to a later one, such as",
		func(pattern string) {
			const chunkSize = 256 << 10
			m, err := grep.Compile(pattern)
			Expect(err).NotTo(HaveOccurred())
			start := clock.Real{}.Now()
			Expect(m.Lines(strings.NewReader(logLines(chunkSize)), func(int, []byte) bool { return true })).To(Succeed())
			Expect(clock.Real{}.Now().Sub(start)).To(BeNumerically("<", 5*time.Second))
		},
		Entry("a class", `\d[^@]*[!?]`),
		Entry("a (?s) dot", `(?s)\d.*[!?]`),
	)
})

var _ = Describe("Compile", Label("grep"), func() {
	It("refuses an invalid pattern, quoting it as given", func() {
		_, err := grep.Compile("foo(", grep.IgnoreCase)
		Expect(err).To(MatchError(ContainSubstring("missing closing ): `foo(`")))
	})

	It("takes the pattern case-insensitively with IgnoreCase, and literally with Literal", func() {
		Expect(hits("a.B", "a.b\naxb\n", grep.IgnoreCase)).To(HaveExactElements("1:a.b", "2:axb"))
		Expect(hits("a.B", "a.b\naxb\na.B\n", grep.Literal)).To(HaveExactElements("3:a.B"))
		Expect(hits("a.B", "a.b\naxb\n", grep.IgnoreCase, grep.Literal)).To(HaveExactElements("1:a.b"))
	})

	DescribeTable("refuses a pattern that matches a newline, which no line holds, such as",
		func(pattern string, flags ...grep.Flag) {
			_, err := grep.Compile(pattern, flags...)
			Expect(err).To(MatchError("the pattern matches a newline, which no line holds"))
		},
		Entry("an escaped newline", `a\nb|c`),
		Entry("a class of only a newline", `[\n]`),
		Entry("a literal newline taken literally", "a\nb", grep.Literal),
	)
})

// failOnce fails its first read with err, and then reads EOF.
type failOnce struct{ err error }

func (f *failOnce) Read([]byte) (int, error) {
	err := f.err
	f.err = nil
	if err == nil {
		err = io.EOF
	}
	return 0, err
}
