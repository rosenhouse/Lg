// Command fakegithub serves recorded runs for trying lg by hand. lg sync
// against it needs LG_GH to name a program that prints any token.
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

// repeated collects each value of a repeatable flag.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, " ") }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)
	return nil
}

type fail struct {
	host, match string
	fault       fakegithub.Fault
}

// parseFail reads HOST,MATCH,KIND[,TIMES].
func parseFail(v string) (fail, bool) {
	fields := strings.Split(v, ",")
	if len(fields) < 3 || len(fields) > 4 || fields[0] != "api" && fields[0] != "blob" {
		return fail{}, false
	}
	f := fail{host: fields[0], match: fields[1]}
	switch kind := fields[2]; kind {
	case "drop":
		f.fault.Drop = true
	case "truncate":
		f.fault.Truncate = true
	case "stall":
		f.fault.Stall = true
	case "truncate+stall":
		f.fault.Truncate, f.fault.Stall = true, true
	default:
		status, err := strconv.Atoi(kind)
		if err != nil || status < 100 || status > 599 {
			return fail{}, false
		}
		f.fault.Status = status
	}
	if len(fields) == 4 {
		times, err := strconv.Atoi(fields[3])
		if err != nil || times < 0 {
			return fail{}, false
		}
		f.fault.Times = times
	}
	return f, true
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fakegithub", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var runs, failFlags repeated
	flags.Var(&runs, "run", "serve run `ID=STAGE` from testdata/recordings (repeatable)")
	flags.Var(&failFlags, "fail", "answer `HOST,MATCH,KIND[,TIMES]`: requests to HOST (api or blob) whose path ends in MATCH get KIND (a status, drop, truncate, stall or truncate+stall), at most TIMES times (repeatable)")
	addr := flags.String("addr", "127.0.0.1:8088", "address of the API host")
	pageCap := flags.Int("page-cap", 0, "page every listing at most `n` per page")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "fakegithub: unexpected argument %q\n", flags.Arg(0))
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
		if _, given := stages[runID]; given {
			_, _ = fmt.Fprintf(stderr, "fakegithub: -run %d given twice\n", runID)
			return 2
		}
		stages[runID] = stage
	}
	var fails []fail
	for _, v := range failFlags {
		f, ok := parseFail(v)
		if !ok {
			_, _ = fmt.Fprintf(stderr, "fakegithub: -fail %q: want HOST,MATCH,KIND[,TIMES] with HOST api or blob and KIND a status, drop, truncate, stall or truncate+stall\n", v)
			return 2
		}
		fails = append(fails, f)
	}
	if *pageCap < 0 {
		_, _ = fmt.Fprintln(stderr, "fakegithub: -page-cap must be 0 or more")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *addr)
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
	for _, f := range fails {
		server.Fail(f.host, f.match, f.fault)
	}

	_, _ = fmt.Fprintf(stdout, "fakegithub: serving %s\n", server.URL())
	<-ctx.Done()
	return 0
}
