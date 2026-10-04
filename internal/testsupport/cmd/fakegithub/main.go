// Command fakegithub serves recorded runs for trying lg by hand.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type runFlags []string

func (r *runFlags) String() string { return strings.Join(*r, " ") }

func (r *runFlags) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fakegithub", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var runs runFlags
	flags.Var(&runs, "run", "serve run `ID=STAGE` from testdata/recordings (repeatable)")
	addr := flags.String("addr", "127.0.0.1:8088", "address of the API host")
	pageCap := flags.Int("page-cap", 0, "page every listing at most `n` per page")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	stages := map[int64]string{}
	for _, r := range runs {
		id, stage, ok := strings.Cut(r, "=")
		runID, err := strconv.ParseInt(id, 10, 64)
		if !ok || err != nil {
			_, _ = fmt.Fprintf(stderr, "fakegithub: -run %q: want ID=STAGE\n", r)
			return 2
		}
		stages[runID] = stage
	}

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "fakegithub: %s\n", err)
		return 1
	}
	server := fakegithub.Listen(l)
	defer server.Close()
	for runID, stage := range stages {
		if err := server.Load(runID, stage); err != nil {
			_, _ = fmt.Fprintf(stderr, "fakegithub: %s\n", err)
			return 1
		}
	}
	server.SetPageCap(*pageCap)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, _ = fmt.Fprintf(stdout, "fakegithub: serving %s\n", server.URL())
	<-ctx.Done()
	return 0
}
