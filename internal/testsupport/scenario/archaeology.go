package scenario

import (
	"strings"
	"time"
)

// FooBar is the text that error archaeology searches for.
const FooBar = "foo bar"

// passArtifact is the recorded artifact pass-artifact.
const passArtifact = 11276272069

// ArchaeologyRuns are the runs of Archaeology. Each has its own SHA, and
// those that log FooBar log it in their job "pass".
type ArchaeologyRuns struct {
	MainAugust, MainSeptember, Feature Run
	// Release3 also holds FooBar in the tar.gz nested in its pass-artifact.
	Release3 Run
	// Fork is a pull_request run from a fork's main, with no pull requests listed.
	Fork Run
	// TitleOnly holds FooBar in its display_title only.
	TitleOnly Run
	// PR42 is a pull_request run of PR 42 from a branch of the repository.
	PR42 Run
	// Rerun was created on 2026-09-02 and re-run on 2026-10-01.
	Rerun Run
}

func (a ArchaeologyRuns) All() []Run {
	return []Run{a.MainAugust, a.MainSeptember, a.Release3, a.Feature, a.Fork, a.TitleOnly, a.PR42, a.Rerun}
}

// Archaeology gives runs 1 to 8, which a search for FooBar on main or
// release-3 in the 30 days before recordings.DefaultNow tells apart.
func Archaeology() ArchaeologyRuns {
	run := func(id int64, stage, created, branch string) Run {
		at, err := time.Parse(time.DateOnly, created)
		if err != nil {
			panic(err)
		}
		r := CloneAt(id, stage, at.Add(12*time.Hour))
		return WithSHA(OnBranch(r, branch), strings.Repeat(string(rune('0'+id)), 40))
	}
	logged := func(r Run) Run { return InjectLogLine(r, 1, "pass", FooBar) }
	return ArchaeologyRuns{
		MainAugust:    logged(run(1, "after-attempt-1", "2026-08-20", "main")),
		MainSeptember: logged(run(2, "after-attempt-1", "2026-09-10", "main")),
		Release3:      WithArtifactZip(logged(run(3, "after-attempt-1", "2026-09-20", "release-3")), 3*idSpace+passArtifact, BuildArtifactZip(FooBar)),
		Feature:       logged(run(4, "after-attempt-1", "2026-09-25", "feat/retry-upload")),
		Fork:          logged(FromFork(WithEvent(run(5, "after-attempt-1", "2026-10-02", "main"), "pull_request"), "someone/Lg")),
		TitleOnly:     WithDisplayTitle(run(6, "after-attempt-1", "2026-10-01", "main"), "Fix "+FooBar),
		PR42:          WithPullRequests(WithEvent(run(7, "after-attempt-1", "2026-09-28", "fix-flake"), "pull_request"), 42),
		Rerun:         RerunAt(run(8, "after-attempt-2", "2026-09-02", "main"), 2, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)),
	}
}
