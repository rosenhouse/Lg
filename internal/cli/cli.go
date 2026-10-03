// Package cli declares lg's commands and maps their errors to exit codes.
package cli

type Deps struct{}

func RealDeps() Deps { return Deps{} }

func Main(args []string, deps Deps) int { return 0 }
