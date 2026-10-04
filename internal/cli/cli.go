// Package cli declares lg's commands and maps their errors to exit codes.
package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/execx"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
)

type Deps struct {
	Env       map[string]string
	Stdout    io.Writer
	Stderr    io.Writer
	Clock     clock.Clock
	Runner    execx.Runner
	NewGitHub func(api *url.URL, repo, token string, clk clock.Clock) github.Client
}

func RealDeps() Deps {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return Deps{Env: env, Stdout: os.Stdout, Stderr: os.Stderr, Clock: clock.Real{}, Runner: execx.Real{}, NewGitHub: github.NewDefault}
}

type commands struct {
	Root    rootCmd    `cmd:"" help:"Print the data directory."`
	Version versionCmd `cmd:"" help:"Print lg's version."`
	Sync    syncCmd    `cmd:"" help:"Mirror the repository's Actions runs into the data directory."`
	Paths   pathsCmd   `cmd:"" help:"Print the paths of mirrored job logs."`
}

// kongExit carries Kong's exit code, as after --help, out of Parse.
type kongExit int

// errWriter remembers its first write error, which Kong reports as a ParseError.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if e.err == nil {
		e.err = err
	}
	return n, err
}

func Main(args []string, deps Deps) (code int) {
	stdout := &errWriter{w: deps.Stdout}
	parser := kong.Must(&commands{},
		kong.Name("lg"),
		kong.Writers(stdout, deps.Stderr),
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
	if stdout.err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: %s\n", stdout.err)
		return 1
	}
	if err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: %s\n", err)
		var parseErr *kong.ParseError
		if errors.As(err, &parseErr) {
			parser.Stdout = deps.Stderr
			_ = parseErr.Context.PrintUsage(true)
		}
		return 2
	}
	deps.Clock, err = clock.FromEnv(deps.Env, deps.Clock)
	if err != nil {
		err = config.Error(err.Error())
	}
	if err == nil {
		err = checkStore(ctx.Command(), deps.Env)
	}
	if err == nil {
		err = ctx.Run(&deps)
	}
	if err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: %s\n", strings.ReplaceAll(err.Error(), "\n", "\nlg: "))
		var configErr config.Error
		switch {
		case errors.As(err, &configErr):
			return 2
		case errors.Is(err, lock.ErrTimeout):
			return 4
		}
		return 1
	}
	return 0
}

// checkStore refuses a store that lg cannot own before any command but version runs.
func checkStore(command string, env map[string]string) error {
	if command == "version" {
		return nil
	}
	roots, err := config.Locations(env)
	if err != nil {
		return err
	}
	return store.Check(roots.Store)
}
