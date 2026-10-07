// Package cli declares lg's commands and maps their errors to exit codes.
package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/execx"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/status"
	"github.com/rosenhouse/lg/internal/store"
)

type Deps struct {
	Env       map[string]string
	Stdout    io.Writer
	Stderr    io.Writer
	Stdin     io.Reader
	Clock     clock.Clock
	Runner    execx.Runner
	NewGitHub func(api *url.URL, repo, token string, clk clock.Clock) github.Client
	StoreFS   store.FS
	GOOS      string
	// Executable gives the path lg was run by, and the file it resolves to.
	Executable func() (path, file string, err error)
}

func RealDeps() Deps {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return Deps{Env: env, Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, Clock: clock.Real{}, Runner: execx.Real{}, NewGitHub: github.NewDefault, StoreFS: store.OSFS{}, GOOS: runtime.GOOS, Executable: func() (string, string, error) { return executable(env["PATH"]) }}
}

type commands struct {
	Init    initCmd    `cmd:"" help:"Write config.yaml for one repository and initialize the store."`
	Root    rootCmd    `cmd:"" help:"Print the data directory."`
	Version versionCmd `cmd:"" help:"Print lg's version."`
	Status  statusCmd  `cmd:"" help:"Print when lg last synced, how far behind it is, and why it is blocked."`
	Sync    syncCmd    `cmd:"" help:"Mirror the repository's Actions runs into the data directory."`
	Gc      gcCmd      `cmd:"" help:"Remove runs older than retention, and the oldest data while over disk_cap."`
	Paths   pathsCmd   `cmd:"" help:"Print the paths of mirrored files, for grep or rg."`
	Where   whereCmd   `cmd:"" help:"Decode a path or an rg hit into JSON."`
	Flakes  flakesCmd  `cmd:"" help:"Report jobs and steps that failed in one attempt of a run and passed in another, or failed alone on the default branch."`
	Index   indexCmd   `cmd:"" help:"Maintain the SQLite index of data/."`
	Daemon  daemonCmd  `cmd:"" help:"Run the daemon that keeps the store fresh."`
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
		kong.Vars{"write_lock_wait": writeLockWait.String(), "cycle_wait": cycleWait.String()},
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
		warn(&deps, ctx.Command())
		err = checkStore(ctx.Command(), deps.Env)
	}
	if err == nil {
		err = ctx.Run(&deps)
	}
	if err != nil {
		if !errors.As(err, new(warned)) {
			printError(deps.Stderr, err)
		}
		var configErr config.Error
		var blocked failure.Blocked
		switch {
		case errors.As(err, &configErr):
			return 2
		case errors.As(err, &blocked):
			return 3
		case errors.Is(err, lock.ErrTimeout):
			return 4
		}
		return 1
	}
	return 0
}

// warn prints the line status.Warning gives for the store's status.json, if
// any, with its hint unless command already runs it.
func warn(deps *Deps, command string) {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return
	}
	st, err := status.Read(filepath.Join(roots.State, "status.json"))
	if err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: warning: %s\n", err)
		return
	}
	warning, hint := status.Warning(deps.Clock.Now(), st)
	runsHint := command == hint || hint == "sync" && command == "daemon run"
	if hint != "" && !runsHint {
		warning += "; run `lg " + hint + "`"
	}
	if warning != "" {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: warning: %s\n", warning)
	}
}

// printError prints each line of err after "lg: ".
func printError(stderr io.Writer, err error) {
	_, _ = fmt.Fprintf(stderr, "lg: %s\n", strings.ReplaceAll(err.Error(), "\n", "\nlg: "))
}

// warned is an error that lg has already printed.
type warned struct{ error }

func (w warned) Unwrap() error { return w.error }

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
