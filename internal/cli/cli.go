// Package cli declares lg's commands and maps their errors to exit codes.
package cli

import "io"

type Deps struct {
	Env    map[string]string
	Stdout io.Writer
	Stderr io.Writer
}

func RealDeps() Deps { return Deps{} }

func Main(args []string, deps Deps) int { return 0 }
