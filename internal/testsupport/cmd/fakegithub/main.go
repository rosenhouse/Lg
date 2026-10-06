// Command fakegithub serves recorded runs for trying lg by hand. lg sync
// against it needs LG_GH to name a program that prints any token.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
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
	// bodyFile holds the body of a 200 fault.
	bodyFile string
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
	case "ratelimit":
		f.fault.Status = http.StatusForbidden
		f.fault.Headers = map[string]string{"X-RateLimit-Remaining": "0"}
	case "secondary":
		f.fault.Status = http.StatusForbidden
		f.fault.Body = fakegithub.SecondaryLimitBody
	default:
		if file, ok := strings.CutPrefix(kind, "body="); ok {
			f.fault.Status, f.bodyFile = http.StatusOK, file
			break
		}
		if seconds, ok := strings.CutPrefix(kind, "retry-after="); ok {
			if _, err := strconv.ParseUint(seconds, 10, 64); err != nil {
				return fail{}, false
			}
			f.fault.Status = http.StatusTooManyRequests
			f.fault.Headers = map[string]string{"Retry-After": seconds}
			break
		}
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

func parseRateLimit(v string) (limit, remaining int, ok bool) {
	l, r, ok := strings.Cut(v, ",")
	limit, errLimit := strconv.Atoi(l)
	remaining, errRemaining := strconv.Atoi(r)
	return limit, remaining, ok && errLimit == nil && errRemaining == nil && 0 <= remaining && remaining <= limit
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fakegithub", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var runs, failFlags, expireFlags repeated
	flags.Var(&runs, "run", "serve run `ID=STAGE` from testdata/recordings (repeatable)")
	flags.Var(&failFlags, "fail", "answer `HOST,MATCH,KIND[,TIMES]`: requests to HOST (api or blob) whose path ends in MATCH get KIND (a status, drop, truncate, stall, truncate+stall, ratelimit for a 403 with none remaining, secondary for a secondary-limit 403, retry-after=SECONDS for a 429, or body=FILE for a 200 of FILE's bytes), at most TIMES times (repeatable); truncate a log or zip on the blob host, since the API hop only redirects")
	flags.Var(&expireFlags, "expire", "list artifact `ID` as expired: true (repeatable)")
	addr := flags.String("addr", "127.0.0.1:8088", "address of the API host")
	pageCap := flags.Int("page-cap", 0, "page every listing at most `n` per page")
	rateLimit := flags.String("rate-limit", "5000,5000", "set X-RateLimit-Limit to LIMIT and X-RateLimit-Remaining to REMAINING before the first request, given as `LIMIT,REMAINING`")
	scenarioName := flags.String("scenario", "", "also serve the runs of the named `scenario`: archaeology (runs 1 to 8)")
	now := flags.String("now", "", "start the clock that Date and X-RateLimit-Reset come from at the RFC 3339 `time`, not the real time")
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
			_, _ = fmt.Fprintf(stderr, "fakegithub: -fail %q: want HOST,MATCH,KIND[,TIMES] with HOST api or blob and KIND a status, drop, truncate, stall, truncate+stall, ratelimit, secondary, retry-after=SECONDS or body=FILE\n", v)
			return 2
		}
		if f.bodyFile != "" {
			body, err := os.ReadFile(f.bodyFile)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "fakegithub: -fail %q: %s\n", v, err)
				return 2
			}
			f.fault.Body = string(body)
		}
		fails = append(fails, f)
	}
	var expired []int64
	for _, v := range expireFlags {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "fakegithub: -expire %q: want an artifact ID\n", v)
			return 2
		}
		expired = append(expired, id)
	}
	if *pageCap < 0 {
		_, _ = fmt.Fprintln(stderr, "fakegithub: -page-cap must be 0 or more")
		return 2
	}
	limit, remaining, ok := parseRateLimit(*rateLimit)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "fakegithub: -rate-limit %q: want LIMIT,REMAINING with 0 <= REMAINING <= LIMIT\n", *rateLimit)
		return 2
	}
	var added []scenario.Run
	switch *scenarioName {
	case "":
	case "archaeology":
		added = scenario.Archaeology().All()
	default:
		_, _ = fmt.Fprintf(stderr, "fakegithub: -scenario %q: want archaeology\n", *scenarioName)
		return 2
	}
	var clk clock.Clock = clock.Real{}
	if *now != "" {
		start, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "fakegithub: -now %q: want an RFC 3339 time\n", *now)
			return 2
		}
		clk = clock.Starting(start, clock.Real{})
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
	listed := map[int64]bool{}
	for runID, stage := range stages {
		if err := server.Load(runID, stage); err != nil {
			_, _ = fmt.Fprintf(stderr, "fakegithub: %s\n", err)
			return 1
		}
		run := scenario.Recorded(runID, stage)
		for _, id := range expired {
			if run.ListsArtifact(id) {
				run, listed[id] = scenario.Expire(run, id), true
			}
		}
		if err := server.AddRun(run); err != nil {
			_, _ = fmt.Fprintf(stderr, "fakegithub: %s\n", err)
			return 1
		}
	}
	for _, run := range added {
		if err := server.AddRun(run); err != nil {
			_, _ = fmt.Fprintf(stderr, "fakegithub: %s\n", err)
			return 1
		}
	}
	for _, id := range expired {
		if !listed[id] {
			_, _ = fmt.Fprintf(stderr, "fakegithub: -expire %d: no -run lists that artifact\n", id)
			return 2
		}
	}
	server.SetPageCap(*pageCap)
	server.SetRateLimit(limit, remaining)
	server.SetClock(clk)
	for _, f := range fails {
		server.Fail(f.host, f.match, f.fault)
	}

	_, _ = fmt.Fprintf(stdout, "fakegithub: serving %s\n", server.URL())
	<-ctx.Done()
	return 0
}
