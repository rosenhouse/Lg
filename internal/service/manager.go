package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rosenhouse/lg/internal/execx"
)

// Manager installs a Unit as a systemd user unit on linux, or as a launchd
// agent on darwin.
type Manager struct {
	GOOS   string
	Runner execx.Runner
	// Env is the user's environment. It locates the unit's dir, and
	// systemctl and launchctl run in it.
	Env map[string]string
	// UID names the launchd domain gui/<UID>.
	UID int
}

// Install writes u's unit file and starts it, restarting a service it replaces.
func (m Manager) Install(ctx context.Context, u Unit) (path string, err error) {
	b, err := m.backend(u.Name)
	if err != nil {
		return "", err
	}
	content, err := b.render(u)
	if err != nil {
		return "", err
	}
	_, err = os.Stat(b.path())
	replacing := err == nil
	if err := b.prepare(ctx, u); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(b.path()), 0o755); err != nil {
		return "", err
	}
	if err := replaceFile(b.path(), content); err != nil {
		return "", err
	}
	return b.path(), b.start(ctx, replacing)
}

// replaceFile writes path through a temp file of its own, so concurrent
// installs never share one.
func replaceFile(path string, content []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	err = errors.Join(err, f.Sync(), f.Close())
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(f.Name()))
	}
	return nil
}

// Uninstall stops the service named name and removes its unit file. It
// gives the path it removed, or "" when there was none.
func (m Manager) Uninstall(ctx context.Context, name string) (path string, err error) {
	b, err := m.backend(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(b.path()); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err := b.stop(ctx); err != nil {
		return "", err
	}
	if err := os.Remove(b.path()); err != nil {
		return "", err
	}
	return b.path(), b.removed(ctx)
}

type backend interface {
	path() string
	render(Unit) ([]byte, error)
	// prepare runs before the unit file is written.
	prepare(ctx context.Context, u Unit) error
	start(ctx context.Context, replacing bool) error
	stop(ctx context.Context) error
	// removed runs after the unit file is removed.
	removed(ctx context.Context) error
}

// validName keeps a name to one plain file name that systemd and launchd accept.
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func (m Manager) backend(name string) (backend, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("invalid service name %q: use letters, digits, '_', '.' and '-'", name)
	}
	home := m.Env["HOME"]
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("HOME must be an absolute path: %q", home)
	}
	switch m.GOOS {
	case "linux":
		config := filepath.Join(home, ".config")
		if xdg := m.Env["XDG_CONFIG_HOME"]; filepath.IsAbs(xdg) {
			config = xdg
		}
		return systemd{m: m, unit: name + ".service", dir: filepath.Join(config, "systemd", "user")}, nil
	case "darwin":
		return launchd{m: m, label: Label(name), dir: filepath.Join(home, "Library", "LaunchAgents")}, nil
	}
	return nil, fmt.Errorf("lg daemon install supports linux and darwin, not %s", m.GOOS)
}

// run runs a command in m.Env, and says what it printed to stderr when it fails.
func (m Manager) run(ctx context.Context, name string, args ...string) error {
	_, stderr, err := m.Runner.Run(ctx, name, args, m.Env)
	if err != nil {
		command := strings.Join(append([]string{name}, args...), " ")
		if msg := string(bytes.TrimSpace(stderr)); msg != "" {
			return fmt.Errorf("%s: %w: %s", command, err, msg)
		}
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}

type systemd struct {
	m         Manager
	unit, dir string
}

func (s systemd) path() string { return filepath.Join(s.dir, s.unit) }

func (systemd) render(u Unit) ([]byte, error) { return RenderSystemd(u) }

func (systemd) prepare(context.Context, Unit) error { return nil }

func (s systemd) start(ctx context.Context, replacing bool) error {
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := s.systemctl(ctx, "enable", "--now", s.unit); err != nil {
		return err
	}
	if replacing {
		return s.systemctl(ctx, "restart", s.unit)
	}
	return nil
}

// stop disables and stops the service. A unit systemd cannot disable, as
// when it never loaded the unit, counts as stopped when it is not running.
func (s systemd) stop(ctx context.Context) error {
	err := s.systemctl(ctx, "disable", "--now", s.unit)
	if err != nil && s.notRunning(ctx) {
		return nil
	}
	return err
}

func (s systemd) notRunning(ctx context.Context) bool {
	out, _, err := s.m.Runner.Run(ctx, "systemctl", []string{"--user", "show", "-p", "ActiveState", "--value", s.unit}, s.m.Env)
	state := string(bytes.TrimSpace(out))
	return err == nil && (state == "inactive" || state == "failed")
}

func (s systemd) removed(ctx context.Context) error { return s.systemctl(ctx, "daemon-reload") }

func (s systemd) systemctl(ctx context.Context, args ...string) error {
	return s.m.run(ctx, "systemctl", append([]string{"--user"}, args...)...)
}

type launchd struct {
	m          Manager
	label, dir string
}

func (l launchd) path() string { return filepath.Join(l.dir, l.label+".plist") }

func (launchd) render(u Unit) ([]byte, error) { return RenderLaunchd(u) }

// prepare boots out a loaded agent, since bootstrap refuses one, and
// creates the log's dir, since launchd does not.
func (l launchd) prepare(ctx context.Context, u Unit) error {
	if err := l.stop(ctx); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Dir(u.Log), 0o755)
}

func (l launchd) start(ctx context.Context, _ bool) error {
	return l.m.run(ctx, "launchctl", "bootstrap", l.domain(), l.path())
}

// stop boots out the agent if it is loaded.
func (l launchd) stop(ctx context.Context) error {
	target := l.domain() + "/" + l.label
	if l.m.run(ctx, "launchctl", "print", target) != nil {
		return nil
	}
	return l.m.run(ctx, "launchctl", "bootout", target)
}

func (launchd) removed(context.Context) error { return nil }

func (l launchd) domain() string { return fmt.Sprintf("gui/%d", l.m.UID) }
