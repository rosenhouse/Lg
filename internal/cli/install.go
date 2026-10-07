package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/service"
)

type daemonInstallCmd struct {
	Name string `hidden:"" default:"lg" help:"Name the unit and label after this."`
}

func (daemonInstallCmd) Help() string {
	return "Writes ${XDG_CONFIG_HOME:-~/.config}/systemd/user/lg.service on Linux or ~/Library/LaunchAgents/com.github.rosenhouse.lg.plist on macOS. " +
		"It bakes in lg's path, the dir of gh, and LG_HOME and LG_CONFIG when set, so run it again after changing any of them; " +
		"reinstalling rewrites the unit and restarts it. " +
		"Logs go to the journal (journalctl --user -u lg) on Linux and to the store's state/daemon.log on macOS."
}

func (c daemonInstallCmd) Run(deps *Deps) error {
	exe, file, err := deps.Executable()
	if err != nil {
		return err
	}
	for _, path := range []string{exe, file} {
		if service.BuiltByGoRun(path) {
			return fmt.Errorf("%s was built by go run; install from go build or go install", path)
		}
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
	configFile, err := config.File(deps.Env)
	if err != nil {
		return err
	}
	env, err := service.Env(deps.GOOS, deps.Env, gh)
	if err != nil {
		return err
	}
	path, err := manager(deps).Install(context.Background(), service.Unit{
		Name: c.Name,
		Exe:  exe,
		Env:  env,
		Log:  filepath.Join(t.roots.State, "daemon.log"),
	})
	if err != nil {
		return err
	}
	// Naming what the service uses shows a reinstall that dropped LG_HOME or LG_CONFIG.
	_, err = fmt.Fprintf(deps.Stdout, "installed %s; store %s, config %s\n", path, t.roots.Store, configFile)
	return err
}

type daemonUninstallCmd struct {
	Name string `hidden:"" default:"lg" help:"Remove the unit and label of this name."`
}

func (c daemonUninstallCmd) Run(deps *Deps) error {
	r, err := manager(deps).Uninstall(context.Background(), c.Name)
	switch {
	case err != nil:
		return err
	case r.Removed:
		_, err = fmt.Fprintf(deps.Stdout, "removed %s\n", r.Path)
	case r.Stopped:
		_, err = fmt.Fprintf(deps.Stdout, "stopped the service named %s, whose unit file %s was missing\n", c.Name, r.Path)
	default:
		_, err = fmt.Fprintf(deps.Stdout, "no service named %s is installed\n", c.Name)
	}
	return err
}

// executable gives the path lg was run by, as service.Executable finds it
// on path, and the file it resolves to.
func executable(path string) (exe, file string, err error) {
	self, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	file, err = filepath.EvalSymlinks(self)
	if err != nil {
		return "", "", err
	}
	return service.Executable(os.Args[0], path, self), file, nil
}

func manager(deps *Deps) service.Manager {
	return service.Manager{GOOS: deps.GOOS, Runner: deps.Runner, Env: deps.Env, UID: os.Getuid(), Clock: deps.Clock}
}
