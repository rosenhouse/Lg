// Package doctest finds the shell code and lg command lines in Markdown.
package doctest

// Block is a fenced code block and the line its fence opens on.
type Block struct {
	Line int
	Text string
}

// ShBlocks gives the fenced code blocks tagged sh.
func ShBlocks(md string) []Block { return nil }

// Command is the arguments of one lg invocation and the line it is on.
type Command struct {
	Line int
	Args []string
}

// LgCommands gives each lg invocation in an sh block or an inline code span.
func LgCommands(md string) []Command { return nil }
