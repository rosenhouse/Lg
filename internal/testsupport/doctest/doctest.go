// Package doctest finds the shell code and lg command lines in Markdown.
package doctest

import (
	"regexp"
	"strings"
)

// Block is a fenced code block and the line its fence opens on.
type Block struct {
	Line int
	Text string
}

// ShBlocks gives the fenced code blocks tagged sh.
func ShBlocks(md string) []Block {
	var blocks []Block
	lines(md, func(n int, line string, f fence) {
		if f.lang != "sh" {
			return
		}
		if len(blocks) == 0 || blocks[len(blocks)-1].Line != f.line {
			blocks = append(blocks, Block{Line: f.line})
		}
		blocks[len(blocks)-1].Text += line + "\n"
	})
	return blocks
}

// Tags gives the tag of each fenced code block, empty when it has none.
func Tags(md string) []string {
	var tags []string
	last := 0
	lines(md, func(n int, line string, f fence) {
		if f.line != 0 && f.line != last {
			tags = append(tags, f.lang)
			last = f.line
		}
	})
	return tags
}

// fence is the language of a fenced code block and the line it opens on.
// The zero fence is outside any block.
type fence struct {
	line int
	lang string
}

// lines calls visit with each line of md that is not a fence, numbered from
// 1, and the block it is in.
func lines(md string, visit func(n int, line string, f fence)) {
	var open fence
	for i, line := range strings.Split(strings.TrimSuffix(md, "\n"), "\n") {
		info, isFence := strings.CutPrefix(strings.TrimLeft(line, " "), "```")
		switch {
		case isFence && open.line == 0:
			lang, _, _ := strings.Cut(strings.TrimSpace(info), " ")
			open = fence{line: i + 1, lang: lang}
		case isFence:
			open = fence{}
		default:
			visit(i+1, line, open)
		}
	}
}

// Command is the arguments of one lg invocation and the line it is on.
type Command struct {
	Line int
	Args []string
}

var inlineCode = regexp.MustCompile("`([^`\n]+)`")

// LgCommands gives each lg invocation in an sh block or an inline code span.
func LgCommands(md string) []Command {
	p := &shell{}
	start, continued := 0, ""
	lines(md, func(n int, line string, f fence) {
		switch {
		case f.line == 0:
			for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
				if strings.HasPrefix(m[1], "lg ") {
					p.parse(n, m[1])
				}
			}
		case f.lang == "sh":
			if continued == "" {
				start = n
			}
			if body, ok := strings.CutSuffix(line, "\\"); ok {
				continued += body
				return
			}
			p.parse(start, continued+line)
			continued = ""
		}
	})
	return p.found
}

// FlagSpans gives the words of each inline code span that starts with "--".
func FlagSpans(md string) []Command {
	var spans []Command
	lines(md, func(n int, line string, f fence) {
		if f.line != 0 {
			return
		}
		for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
			if strings.HasPrefix(m[1], "--") {
				spans = append(spans, Command{Line: n, Args: strings.Fields(m[1])})
			}
		}
	})
	return spans
}

// shell finds lg invocations in lines of shell, as far as SKILL.md needs.
type shell struct {
	found []Command
}

// keywords may precede a command.
var keywords = map[string]bool{"if": true, "!": true}

func (p *shell) parse(line int, s string) {
	for _, words := range p.commands(line, s) {
		for len(words) > 0 && keywords[words[0]] {
			words = words[1:]
		}
		if len(words) > 0 && words[0] == "lg" {
			p.found = append(p.found, Command{Line: line, Args: words[1:]})
		}
	}
}

// commands splits s into the words of each simple command, and parses each
// command substitution in it.
func (p *shell) commands(line int, s string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
		}
		word.Reset()
		inWord = false
	}
	endCommand := func() {
		endWord()
		commands = append(commands, words)
		words = nil
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			endWord()
		case c == '#' && !inWord:
			endCommand()
			return commands
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'') + i + 1
			if end == i {
				end = len(s)
			}
			word.WriteString(s[i+1 : end])
			inWord, i = true, end
		case c == '"':
			i = p.doubleQuoted(line, s, i+1, &word)
			inWord = true
		case c == '\\' && i+1 < len(s):
			word.WriteByte(s[i+1])
			inWord, i = true, i+1
		case strings.HasPrefix(s[i:], "$("):
			i = p.substitution(line, s, i+2, &word)
			inWord = true
		case strings.IndexByte("|&;()<>", c) >= 0:
			if c == '>' || c == '<' {
				// A file descriptor number belongs to the redirection.
				if w := word.String(); inWord && strings.Trim(w, "0123456789") == "" {
					word.Reset()
					inWord = false
				}
			}
			endCommand()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endCommand()
	return commands
}

// doubleQuoted writes the quoted text starting at s[i] to word, and gives the
// index of the closing quote.
func (p *shell) doubleQuoted(line int, s string, i int, word *strings.Builder) int {
	for ; i < len(s) && s[i] != '"'; i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			word.WriteByte(s[i+1])
			i++
		case strings.HasPrefix(s[i:], "$("):
			i = p.substitution(line, s, i+2, word)
		default:
			word.WriteByte(s[i])
		}
	}
	return i
}

// substitution parses the command substitution whose body starts at s[i],
// writes a placeholder for it to word, and gives the index of its ")".
func (p *shell) substitution(line int, s string, i int, word *strings.Builder) int {
	depth := 1
	end := i
	for ; end < len(s); end++ {
		switch s[end] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	p.parse(line, s[i:end])
	word.WriteString("$(…)")
	return end
}
