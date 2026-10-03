package cli

import (
	"fmt"

	"github.com/rosenhouse/lg/internal/config"
)

type rootCmd struct{}

func (rootCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Stdout, roots.Data)
	return err
}
