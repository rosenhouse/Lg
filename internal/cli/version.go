package cli

import (
	"fmt"

	"github.com/rosenhouse/lg/internal/version"
)

type versionCmd struct{}

func (versionCmd) Run(deps *Deps) error {
	_, err := fmt.Fprintln(deps.Stdout, version.Get())
	return err
}
