package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/config"
)

type pathsCmd struct {
	Branch     []string `help:"Only runs on this branch of the repository, not of a fork."`
	SHA        []string `name:"sha" help:"Only runs of a commit whose SHA starts with this."`
	PR         []int    `name:"pr" help:"Only runs of this pull request."`
	Workflow   []string `help:"Only runs of this workflow."`
	Job        []string `help:"Only jobs whose name matches this glob."`
	Event      []string `help:"Only runs triggered by this event."`
	Conclusion []string `help:"Only units with this conclusion."`
	Since      string   `help:"Only units since this time."`
	Until      string   `help:"Only units until this time."`
	Unit       *string  `enum:"run,attempt,job,log,artifact,extracted" help:"Print the files of this unit."`
	Null       bool     `short:"0" help:"Separate paths with NUL instead of newline."`
}

func (p pathsCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	sep := "\n"
	if p.Null {
		sep = "\x00"
	}
	if _, err := os.Lstat(roots.Data); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// The trailing separator makes WalkDir follow a symlinked data/.
	return filepath.WalkDir(roots.Data+string(filepath.Separator), func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || d.Name() != "log.txt" {
			return err
		}
		_, err = fmt.Fprint(deps.Stdout, path, sep)
		return err
	})
}
