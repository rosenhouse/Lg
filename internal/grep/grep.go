// Package grep finds the lines of a file that a regular expression matches.
package grep

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"regexp/syntax"
	"slices"
	"sync"
	"unicode/utf8"
)

const (
	// binarySniff is how many leading bytes of a file Lines checks for NUL.
	binarySniff = 8 << 10
	// chunkSize is how many bytes Lines reads at once, unless a line is longer.
	chunkSize = 256 << 10
)

// chunks holds buffers for Lines, since allocating one for each small file
// would take longer than searching it.
var chunks = sync.Pool{New: func() any { return new([chunkSize]byte) }}

// Matcher finds the lines that a pattern matches.
type Matcher struct {
	// lines matches what the pattern matches in a line, but never a newline,
	// and its ^, $, \A and \z match at line ends. So a match in a run of
	// lines lies within one line, which the pattern matches.
	lines *regexp.Regexp
	// Every match holds one of literals, lowered when fold, unless it is nil.
	literals [][]byte
	fold     bool
}

// Flag changes how Compile takes a pattern.
type Flag syntax.Flags

const (
	IgnoreCase = Flag(syntax.FoldCase)
	Literal    = Flag(syntax.Literal)
)

var errNewline = errors.New("the pattern matches a newline, which no line holds")

// Compile parses pattern in Go's RE2 syntax.
func Compile(pattern string, flags ...Flag) (*Matcher, error) {
	parse := syntax.Perl
	for _, f := range flags {
		parse |= syntax.Flags(f)
	}
	re, err := syntax.Parse(pattern, parse)
	if err != nil {
		return nil, err
	}
	if err := lineOnly(re); err != nil {
		return nil, err
	}
	lines, err := regexp.Compile(re.String())
	if err != nil {
		return nil, err
	}
	m := &Matcher{lines: lines}
	m.literals, m.fold = requiredLiterals(re)
	return m, nil
}

// lineOnly rewrites re as Matcher.lines takes it. It refuses a newline that
// re must match.
func lineOnly(re *syntax.Regexp) error {
	for _, sub := range re.Sub {
		if err := lineOnly(sub); err != nil {
			return err
		}
	}
	switch re.Op {
	case syntax.OpBeginText:
		re.Op = syntax.OpBeginLine
	case syntax.OpEndText:
		re.Op = syntax.OpEndLine
	case syntax.OpAnyChar:
		re.Op = syntax.OpAnyCharNotNL
	case syntax.OpLiteral:
		if slices.Contains(re.Rune, '\n') {
			return errNewline
		}
	case syntax.OpCharClass:
		re.Rune = withoutNewline(re.Rune)
	}
	return nil
}

// withoutNewline removes '\n' from the ranges of a character class.
func withoutNewline(ranges []rune) []rune {
	var out []rune
	for i := 0; i < len(ranges); i += 2 {
		lo, hi := ranges[i], ranges[i+1]
		if lo <= '\n' && '\n' <= hi {
			if lo < '\n' {
				out = append(out, lo, '\n'-1)
			}
			if '\n' < hi {
				out = append(out, '\n'+1, hi)
			}
			continue
		}
		out = append(out, lo, hi)
	}
	return out
}

// requiredLiterals gives literals that every match of re holds one of,
// lowered when they match case-insensitively, or nil.
func requiredLiterals(re *syntax.Regexp) (literals [][]byte, fold bool) {
	set := literalSet(re)
	if set == nil {
		return nil, false
	}
	fold = set[0].Flags&syntax.FoldCase != 0
	for _, lit := range set {
		litFold := lit.Flags&syntax.FoldCase != 0
		switch {
		// Literals the prefilter cannot find: those that differ in case
		// sensitivity, and U+FFFD, which also matches invalid UTF-8.
		case litFold != fold, slices.Contains(lit.Rune, utf8.RuneError):
			return nil, false
		case !fold:
			literals = append(literals, []byte(string(lit.Rune)))
		// lowerASCII lowers only ASCII.
		case slices.ContainsFunc(lit.Rune, func(r rune) bool { return r >= utf8.RuneSelf }):
			return nil, false
		default:
			literals = append(literals, lowerASCII(nil, []byte(string(lit.Rune))))
		}
	}
	return literals, fold
}

// literalSet gives literals that every match of re holds one of, or nil.
// Of a concatenation's sets it gives the one whose shortest literal is longest.
func literalSet(re *syntax.Regexp) []*syntax.Regexp {
	switch re.Op {
	case syntax.OpLiteral:
		return []*syntax.Regexp{re}
	case syntax.OpCapture:
		return literalSet(re.Sub[0])
	case syntax.OpConcat:
		var best []*syntax.Regexp
		for _, sub := range re.Sub {
			if set := literalSet(sub); set != nil && (best == nil || shortest(set) > shortest(best)) {
				best = set
			}
		}
		return best
	case syntax.OpAlternate:
		var all []*syntax.Regexp
		for _, sub := range re.Sub {
			set := literalSet(sub)
			if set == nil {
				return nil
			}
			all = append(all, set...)
		}
		return all
	}
	return nil
}

func shortest(set []*syntax.Regexp) int {
	n := len(set[0].Rune)
	for _, lit := range set[1:] {
		n = min(n, len(lit.Rune))
	}
	return n
}

func lowerASCII(dst, src []byte) []byte {
	dst = slices.Grow(dst[:0], len(src))[:len(src)]
	for i, b := range src {
		dst[i] = lower[b]
	}
	return dst
}

// lower maps each byte to itself, but A to Z to a to z.
var lower = func() (t [256]byte) {
	for i := range t {
		t[i] = byte(i)
		if 'A' <= i && i <= 'Z' {
			t[i] += 'a' - 'A'
		}
	}
	return t
}()

// foldsToASCII are the non-ASCII runes that match an ASCII letter
// case-insensitively: long s matches s, and the Kelvin sign k.
var foldsToASCII = [][]byte{[]byte("\u017f"), []byte("\u212a")}

// Lines calls hit with each line of r that m matches, numbered from 1,
// without its newline, until hit returns false. It strips a leading UTF-8 BOM,
// and reads no line of a binary file: one with a NUL byte in its first 8 KiB.
func (m *Matcher) Lines(r io.Reader, hit func(n int, text []byte) bool) error {
	chunk := chunks.Get().(*[chunkSize]byte)
	defer chunks.Put(chunk)
	buf := chunk[:0]
	var lowered []byte
	for n, first := 1, true; ; first = false {
		read, err := io.ReadFull(r, buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+read]
		eof := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if eof {
			err = nil
		}
		if first {
			if bytes.IndexByte(buf[:min(len(buf), binarySniff)], 0) >= 0 {
				return err
			}
			buf = buf[:copy(buf, bytes.TrimPrefix(buf, []byte("\ufeff")))]
		}
		lines := buf
		if !eof {
			lines = buf[:bytes.LastIndexByte(buf, '\n')+1]
			if len(lines) == 0 && err == nil {
				buf = slices.Grow(buf, cap(buf))
				continue
			}
		}
		if !m.eachHit(lines, &n, &lowered, hit) || eof || err != nil {
			return err
		}
		buf = buf[:copy(buf, buf[len(lines):])]
	}
}

// eachHit calls hit with each line in lines that m matches, until hit returns
// false. Each line ends in a newline but the last of a file. n is the number
// of the first line, and eachHit moves it past lines.
func (m *Matcher) eachHit(lines []byte, n *int, lowered *[]byte, hit func(n int, text []byte) bool) bool {
	next := m.candidates(lines, lowered)
	counted := 0
	for at := 0; at < len(lines); {
		found := next(at)
		if found < 0 {
			break
		}
		start := bytes.LastIndexByte(lines[:found], '\n') + 1
		if start == len(lines) {
			break
		}
		end := len(lines)
		if i := bytes.IndexByte(lines[found:], '\n'); i >= 0 {
			end = found + i
		}
		if m.lines.Match(lines[start:end]) {
			*n += bytes.Count(lines[counted:start], []byte("\n"))
			counted = start
			if !hit(*n, lines[start:end]) {
				return false
			}
		}
		at = end + 1
	}
	*n += bytes.Count(lines[counted:], []byte("\n"))
	return true
}

// candidates gives a func that gives the first offset in lines, from at, in
// a line that m may match, or -1. It lowers lines into lowered when m.fold.
func (m *Matcher) candidates(lines []byte, lowered *[]byte) func(at int) int {
	in := lines
	switch {
	case m.literals == nil, m.fold && slices.ContainsFunc(foldsToASCII, func(r []byte) bool { return bytes.Contains(lines, r) }):
		return func(at int) int {
			loc := m.lines.FindIndex(lines[at:])
			if loc == nil {
				return -1
			}
			return at + loc[0]
		}
	case m.fold:
		*lowered = lowerASCII(*lowered, lines)
		in = *lowered
	}
	// next[i] is the offset of literals[i] found last, or -1 when none is left.
	next := make([]int, len(m.literals))
	for i, literal := range m.literals {
		next[i] = indexFrom(in, literal, 0)
	}
	return func(at int) int {
		first := -1
		for i, literal := range m.literals {
			if 0 <= next[i] && next[i] < at {
				next[i] = indexFrom(in, literal, at)
			}
			if next[i] >= 0 && (first < 0 || next[i] < first) {
				first = next[i]
			}
		}
		return first
	}
}

// indexFrom gives the offset of the first sep in s from at, or -1.
func indexFrom(s, sep []byte, at int) int {
	i := bytes.Index(s[at:], sep)
	if i < 0 {
		return -1
	}
	return at + i
}
