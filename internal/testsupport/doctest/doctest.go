// Package doctest finds the shell code and lg command lines in Markdown.
package doctest

import "strings"

// Block is a fenced code block and the line its fence opens on.
type Block struct {
	Line int
	Text string
}

// ShBlocks gives the fenced code blocks tagged sh.
func ShBlocks(md string) []Block {
	var blocks []Block
	for _, f := range fences(md) {
		if f.lang == "sh" {
			blocks = append(blocks, f.Block)
		}
	}
	return blocks
}

type fence struct {
	Block
	lang string
}

func fences(md string) []fence {
	var found []fence
	var open *fence
	for i, line := range strings.SplitAfter(md, "\n") {
		info, isFence := strings.CutPrefix(strings.TrimLeft(line, " "), "```")
		switch {
		case open == nil && isFence:
			lang, _, _ := strings.Cut(strings.TrimSpace(info), " ")
			open = &fence{Block: Block{Line: i + 1}, lang: lang}
		case open != nil && isFence:
			found = append(found, *open)
			open = nil
		case open != nil:
			open.Text += line
		}
	}
	return found
}

// Command is the arguments of one lg invocation and the line it is on.
type Command struct {
	Line int
	Args []string
}

// LgCommands gives each lg invocation in an sh block or an inline code span.
func LgCommands(md string) []Command { return nil }
