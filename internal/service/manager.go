package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
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

// Install writes u's unit file and starts it, restarting a service it
// replaces. It does all that can fail before it stops that service.
func (m Manager) Install(ctx context.Context, u Unit) (path string, err error) {
	b, err := m.backend(u.Name)
	if err != nil {
		return "", err
	}
	content, err := b.render(u)
	if err != nil {
		return "", err
	}
	running, err := b.prepare(ctx, u)
	if err != nil {
		return "", err
	}
	tmp, err := writeTemp(b.path(), content)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := b.unload(ctx); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, b.path()); err != nil {
		return "", err
	}
	return b.path(), b.start(ctx, running)
}

// writeTemp writes content to a private temp file beside path, since a
// unit may carry proxy credentials and concurrent installs must not share one.
func writeTemp(path string, content []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	_, err = f.Write(content)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return "", errors.Join(err, os.Remove(f.Name()))
	}
	return f.Name(), nil
}

// Removal is what Uninstall did.
type Removal struct {
	// Path is the unit file's path.
	Path string
	// Removed says Uninstall removed the unit file.
	Removed bool
	// Stopped says Uninstall stopped a service whose unit file was missing.
	Stopped bool
}

// Uninstall stops the service named name and removes its unit file. When
// the unit file is missing, it stops the service if it still runs.
func (m Manager) Uninstall(ctx context.Context, name string) (Removal, error) {
	b, err := m.backend(name)
	if err != nil {
		return Removal{}, err
	}
	r := Removal{Path: b.path()}
	if _, err := os.Stat(b.path()); errors.Is(err, fs.ErrNotExist) {
		r.Stopped, err = b.stopOrphan(ctx)
		return r, err
	}
	if err := b.stop(ctx); err != nil {
		return r, err
	}
	if err := os.Remove(b.path()); err != nil {
		return r, err
	}
	r.Removed = true
	return r, b.removed(ctx)
}

type backend interface {
	path() string
	render(Unit) ([]byte, error)
	// prepare does what can fail before Install changes anything, and
	// reports whether the service runs.
	prepare(ctx context.Context, u Unit) (running bool, err error)
	// unload runs before Install replaces the unit file.
	unload(ctx context.Context) error
	start(ctx context.Context, running bool) error
	stop(ctx context.Context) error
	// removed runs after Uninstall removes the unit file.
	removed(ctx context.Context) error
	// stopOrphan stops a service whose unit file is missing, and reports
	// whether one ran.
	stopOrphan(ctx context.Context) (bool, error)
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
		return launchd{m: m, label: label(name), dir: filepath.Join(home, "Library", "LaunchAgents")}, nil
	}
	return nil, fmt.Errorf("lg daemon install supports linux and darwin, not %s", m.GOOS)
}

// run runs a command in m.Env, and says what it printed to stderr when it fails.
func (m Manager) run(ctx context.Context, name string, args ...string) error {
	_, err := m.output(ctx, name, args...)
	return err
}

// output is run, giving what the command printed to stdout.
func (m Manager) output(ctx context.Context, name string, args ...string) (string, error) {
	stdout, stderr, err := m.Runner.Run(ctx, name, args, m.Env)
	if err != nil {
		command := strings.Join(append([]string{name}, args...), " ")
		if msg := string(bytes.TrimSpace(stderr)); msg != "" {
			return "", fmt.Errorf("%s: %w: %s", command, err, msg)
		}
		return "", fmt.Errorf("%s: %w", command, err)
	}
	return string(stdout), nil
}

type systemd struct {
	m         Manager
	unit, dir string
}

func (s systemd) path() string { return filepath.Join(s.dir, s.unit) }

func (systemd) render(u Unit) ([]byte, error) { return RenderSystemd(u) }

// prepare checks that a user manager is reachable and loads units from
// s.dir, which a shell's XDG_CONFIG_HOME can move.
func (s systemd) prepare(ctx context.Context, _ Unit) (bool, error) {
	out, err := s.output(ctx, "show", "-p", "UnitPath", "--value")
	if err != nil {
		return false, fmt.Errorf("no systemd user manager is reachable: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return false, err
	}
	if !slices.ContainsFunc(splitQuoted(out), func(dir string) bool { return sameFile(dir, s.dir) }) {
		return false, fmt.Errorf("the systemd user manager does not load units from %s; give lg the XDG_CONFIG_HOME the manager has", s.dir)
	}
	if err := s.notElsewhere(ctx); err != nil {
		return false, err
	}
	state, err := s.activeState(ctx)
	return running(state), err
}

// splitQuoted splits a list as systemctl show prints one: on whitespace,
// except inside double quotes, where a backslash escapes the next byte or
// starts a C escape such as \t or \001.
func splitQuoted(s string) []string {
	var words []string
	var word []byte
	inWord, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted && c == '\\' && i+1 < len(s):
			i++
			var n int
			c, n = unescape(s[i:])
			i += n - 1
		case c == '"':
			quoted, inWord = !quoted, true
			continue
		case !quoted && strings.IndexByte(" \t\n", c) >= 0:
			if inWord {
				words, word, inWord = append(words, string(word)), nil, false
			}
			continue
		}
		word, inWord = append(word, c), true
	}
	if inWord {
		words = append(words, string(word))
	}
	return words
}

// unescape decodes the escape after a backslash at the start of s, and
// says how many bytes it took.
func unescape(s string) (byte, int) {
	if c, ok := map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v'}[s[0]]; ok {
		return c, 1
	}
	if len(s) >= 3 {
		if v, err := strconv.ParseUint(s[:3], 8, 8); err == nil {
			return byte(v), 3
		}
	}
	return s[0], 1
}

func (systemd) unload(context.Context) error { return nil }

func (s systemd) start(ctx context.Context, running bool) error {
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := s.systemctl(ctx, "enable", "--now", s.unit); err != nil {
		return err
	}
	if running {
		return s.systemctl(ctx, "restart", s.unit)
	}
	return nil
}

// stop disables and stops the service. A unit systemd cannot disable, as
// when it never loaded the unit, counts as stopped when it is not running.
// Without systemctl, no user manager can run it.
func (s systemd) stop(ctx context.Context) error {
	err := s.systemctl(ctx, "disable", "--now", s.unit)
	if err == nil || errors.Is(err, exec.ErrNotFound) {
		return nil
	}
	if state, stateErr := s.activeState(ctx); stateErr == nil && !running(state) {
		return nil
	}
	return err
}

func (s systemd) removed(ctx context.Context) error {
	err := s.systemctl(ctx, "daemon-reload")
	if errors.Is(err, exec.ErrNotFound) {
		return nil
	}
	return err
}

// stopOrphan stops a service whose unit file was deleted, which systemd
// cannot disable, and removes the wants link enabling it left.
func (s systemd) stopOrphan(ctx context.Context) (bool, error) {
	if err := s.notElsewhere(ctx); err != nil {
		return false, err
	}
	if state, err := s.activeState(ctx); err != nil || !running(state) {
		return false, nil
	}
	if err := s.systemctl(ctx, "stop", s.unit); err != nil {
		return false, err
	}
	wants := filepath.Join(s.dir, "default.target.wants", s.unit)
	if _, err := os.Stat(wants); errors.Is(err, fs.ErrNotExist) {
		if err := os.Remove(wants); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return true, err
		}
	}
	return true, s.systemctl(ctx, "daemon-reload")
}

// notElsewhere refuses when the user manager loads the unit from a file
// other than s.path(), as when the unit was installed with another
// XDG_CONFIG_HOME.
func (s systemd) notElsewhere(ctx context.Context) error {
	// A manager that cannot say, or a fragment since deleted, names no other file.
	out, _ := s.output(ctx, "show", "-p", "FragmentPath", "--value", s.unit)
	fragment := strings.TrimSpace(out)
	if _, err := os.Stat(fragment); err != nil || sameFile(fragment, s.path()) {
		return nil
	}
	return fmt.Errorf("the systemd user manager loads %s from %s, not %s; give lg the XDG_CONFIG_HOME it was installed with", s.unit, fragment, s.path())
}

func (s systemd) activeState(ctx context.Context) (string, error) {
	out, err := s.output(ctx, "show", "-p", "ActiveState", "--value", s.unit)
	return strings.TrimSpace(out), err
}

// running reports whether a unit in ActiveState state runs or is starting.
func running(state string) bool { return state != "inactive" && state != "failed" }

func (s systemd) systemctl(ctx context.Context, args ...string) error {
	_, err := s.output(ctx, args...)
	return err
}

func (s systemd) output(ctx context.Context, args ...string) (string, error) {
	return s.m.output(ctx, "systemctl", append([]string{"--user"}, args...)...)
}

type launchd struct {
	m          Manager
	label, dir string
}

func (l launchd) path() string { return filepath.Join(l.dir, l.label+".plist") }

func (launchd) render(u Unit) ([]byte, error) { return RenderLaunchd(u) }

// prepare creates the log's dir, since launchd does not.
func (launchd) prepare(_ context.Context, u Unit) (bool, error) {
	return false, os.MkdirAll(filepath.Dir(u.Log), 0o755)
}

// unload boots out a loaded agent, since bootstrap refuses one.
func (l launchd) unload(ctx context.Context) error { return l.stop(ctx) }

func (l launchd) start(ctx context.Context, _ bool) error {
	if err := l.m.run(ctx, "launchctl", "bootstrap", l.domain(), l.path()); err != nil {
		return fmt.Errorf("%w; the agent is not loaded", err)
	}
	return nil
}

// unloadWait bounds how long launchd may take to unload a booted-out agent.
const unloadWait = 10 * time.Second

// stop boots out the agent if it is loaded, and waits until launchd has
// unloaded it.
func (l launchd) stop(ctx context.Context) error {
	if !l.loaded(ctx) {
		return nil
	}
	if err := l.m.run(ctx, "launchctl", "bootout", l.target()); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, unloadWait)
	defer cancel()
	stillLoaded := fmt.Errorf("launchd has %s still loaded after bootout", l.target())
	for l.loaded(ctx) {
		select {
		case <-ctx.Done():
			return stillLoaded
		case <-(clock.Real{}).After(100 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		// print failed because ctx ended, not because launchd unloaded the agent.
		return stillLoaded
	}
	return nil
}

func (launchd) removed(context.Context) error { return nil }

func (l launchd) stopOrphan(ctx context.Context) (bool, error) {
	if !l.loaded(ctx) {
		return false, nil
	}
	return true, l.stop(ctx)
}

func (l launchd) loaded(ctx context.Context) bool {
	return l.m.run(ctx, "launchctl", "print", l.target()) == nil
}

func (l launchd) target() string { return l.domain() + "/" + l.label }

func (l launchd) domain() string { return fmt.Sprintf("gui/%d", l.m.UID) }
