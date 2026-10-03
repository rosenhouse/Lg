// Package cli declares lg's commands and maps their errors to exit codes.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

type Deps struct {
	Env    map[string]string
	Stdout io.Writer
	Stderr io.Writer
}

func RealDeps() Deps {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return Deps{Env: env, Stdout: os.Stdout, Stderr: os.Stderr}
}

type commands struct {
	Root    rootCmd    `cmd:"" help:"Print the data directory."`
	Version versionCmd `cmd:"" help:"Print lg's version."`
}

// kongExit carries Kong's exit code, as after --help, out of Parse.
type kongExit int

func Main(args []string, deps Deps) (code int) {
	parser := kong.Must(&commands{},
		kong.Name("lg"),
		kong.Writers(deps.Stdout, deps.Stderr),
		kong.Exit(func(c int) { panic(kongExit(c)) }))
	defer func() {
		if r := recover(); r != nil {
			c, ok := r.(kongExit)
			if !ok {
				panic(r)
			}
			code = int(c)
		}
	}()

	ctx, err := parser.Parse(args)
	if err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: %s\n", err)
		var parseErr *kong.ParseError
		if errors.As(err, &parseErr) {
			parser.Stdout = deps.Stderr
			_ = parseErr.Context.PrintUsage(true)
		}
		return 2
	}
	if err := ctx.Run(&deps); err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: %s\n", err)
		return 1
	}
	return 0
}
