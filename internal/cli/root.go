package cli

import (
	"fmt"

	"github.com/rosenhouse/lg/internal/config"
)

type rootCmd struct{}

func (rootCmd) Help() string {
	return "Prints $LG_HOME/data. Runs are under <host>/<owner>/<repo>/runs/<date>/<run_id>_<workflow>_<branch>/, " +
		"where <owner>/<repo> takes GitHub's spelling, which may differ in case from config.yaml."
}

func (rootCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Stdout, roots.Data)
	return err
}
