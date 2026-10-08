package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/grep"
	"github.com/rosenhouse/lg/internal/index"
)

type grepCmd struct {
	Pattern          string `arg:"" help:"A regular expression in Go's RE2 syntax, such as 'foo.*bar'."`
	filters          `embed:""`
	Unit             []string `sep:"none" enum:"run,attempt,job,log,extracted" help:"Search the files of this unit (${enum}) instead of logs and extracted files."`
	IgnoreCase       bool     `short:"i" help:"Match case-insensitively."`
	FixedStrings     bool     `short:"F" help:"Match PATTERN as a literal string."`
	FilesWithMatches bool     `short:"l" help:"Print the path of each file that matches, instead of its hits."`
	JSON             bool     `name:"json" help:"Print each hit, or with -l each file, as lg where prints it."`
}

func (grepCmd) Help() string {
	return "Searches the files lg paths prints for the same filters and --unit, oldest first, and prints each matching line as path:line:text, as rg -Hn does. " +
		"It strips a UTF-8 BOM from line 1 and skips binary files: those with a NUL byte in their first 8 KiB. " +
		"It exits 5, saying how many files it searched, when no line matches. " +
		"Search with rg's other features through lg paths -0 | xargs -0 -r rg --no-config -Hn."
}

func (g grepCmd) Validate() error {
	if len(g.Unit) > 1 {
		return errors.New("--unit must not be given more than once")
	}
	if _, err := g.matcher(); err != nil {
		return err
	}
	return g.validate()
}

func (g grepCmd) matcher() (*grep.Matcher, error) {
	pattern := g.Pattern
	if g.FixedStrings {
		pattern = regexp.QuoteMeta(pattern)
	}
	if g.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	return grep.Compile(pattern)
}

func (g grepCmd) Run(deps *Deps) error {
	m, err := g.matcher()
	if err != nil {
		return err
	}
	var unit index.Unit
	if len(g.Unit) > 0 {
		unit = index.Unit(g.Unit[0])
	}
	out := bufio.NewWriter(deps.Stdout)
	s := &searcher{matcher: m, open: openReader, stderr: deps.Stderr, filesOnly: g.FilesWithMatches}
	err = query(deps, func(ctx context.Context, ix *index.Index, roots config.Roots) error {
		paths, unread := ix.Paths(ctx, g.filter(deps.Clock.Now()), unit)
		s.print = textHits(out)
		if g.JSON {
			finder := newPlaceFinder(roots.Data)
			defer finder.close()
			s.print = jsonHits(out, finder)
		}
		return errors.Join(unread, s.search(paths))
	})
	if err = errors.Join(err, out.Flush()); err != nil {
		return err
	}
	if s.hits == 0 {
		return noMatch{files: s.files}
	}
	return nil
}

// noMatch is lg grep's error when it found no hit in the files it searched.
type noMatch struct{ files int }

func (n noMatch) Error() string {
	if n.files == 1 {
		return "no match in 1 file"
	}
	return fmt.Sprintf("no match in %d files", n.files)
}

func openReader(path string) (io.ReadCloser, error) { return os.Open(path) }

// searcher prints the lines of files that matcher matches.
type searcher struct {
	matcher *grep.Matcher
	open    func(path string) (io.ReadCloser, error)
	// print prints a hit, or a file when line is 0.
	print     func(path string, line int, text []byte) error
	filesOnly bool
	stderr    io.Writer
	// files counts the files searched, and hits the hits printed.
	files, hits int
}

// search prints the hits in each file of paths. It notes on stderr each file
// it cannot read, and then fails with warned.
func (s *searcher) search(paths []string) error {
	var failed error
	for _, path := range paths {
		s.files++
		var printErr error
		err := s.searchFile(path, func(line int, text []byte) bool {
			s.hits++
			if s.filesOnly {
				line, text = 0, nil
			}
			printErr = s.print(path, line, text)
			return printErr == nil && !s.filesOnly
		})
		if printErr != nil {
			return printErr
		}
		if err != nil {
			printError(s.stderr, err)
			failed = warned{err}
		}
	}
	return failed
}

func (s *searcher) searchFile(path string, hit func(line int, text []byte) bool) error {
	f, err := s.open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return s.matcher.Lines(f, hit)
}

// textHits prints a hit as path:line:text, and a file as its path.
func textHits(w io.Writer) func(path string, line int, text []byte) error {
	return func(path string, line int, text []byte) error {
		if line == 0 {
			_, err := fmt.Fprintln(w, path)
			return err
		}
		_, err := fmt.Fprintf(w, "%s:%d:%s\n", path, line, text)
		return err
	}
}

// jsonHits prints a hit, or a file, as lg where does.
func jsonHits(w io.Writer, finder *placeFinder) func(path string, line int, text []byte) error {
	out := json.NewEncoder(w)
	out.SetEscapeHTML(false)
	return func(path string, line int, text []byte) error {
		p, err := finder.at(path)
		if err != nil {
			return err
		}
		p.Line, p.Text = line, string(text)
		return out.Encode(p)
	}
}
