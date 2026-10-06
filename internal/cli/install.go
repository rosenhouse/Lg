package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/service"
)

type daemonInstallCmd struct {
	Name string `hidden:"" default:"lg" help:"Name the unit and label after this."`
}

func (c daemonInstallCmd) Run(deps *Deps) error {
	exe, err := deps.Executable()
	if err != nil {
		return err
	}
	if service.BuiltByGoRun(exe) {
		return fmt.Errorf("%s was built by go run; install from go build or go install", exe)
	}
	gh, err := service.FindGH(deps.Env)
	if err != nil {
		return err
	}
	// A daemon without a valid config.yaml would exit at once, and restart forever.
	t, err := loadTarget(deps.Env)
	if err != nil {
		return err
	}
	path, err := manager(deps).Install(context.Background(), service.Unit{
		Name: c.Name,
		Exe:  exe,
		Env:  service.Env(deps.Env, gh),
		Log:  filepath.Join(t.roots.State, "daemon.log"),
	})
	if path != "" {
		_, _ = fmt.Fprintf(deps.Stdout, "installed %s\n", path)
	}
	return err
}

type daemonUninstallCmd struct {
	Name string `hidden:"" default:"lg" help:"Name of the unit and label to remove."`
}

func (c daemonUninstallCmd) Run(deps *Deps) error {
	path, err := manager(deps).Uninstall(context.Background(), c.Name)
	switch {
	case err != nil:
		return err
	case path == "":
		_, err = fmt.Fprintf(deps.Stdout, "no service named %s is installed\n", c.Name)
	default:
		_, err = fmt.Fprintf(deps.Stdout, "removed %s\n", path)
	}
	return err
}

func manager(deps *Deps) service.Manager {
	return service.Manager{GOOS: deps.GOOS, Runner: deps.Runner, Env: deps.Env, UID: os.Getuid()}
}
