package fakeservice

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
)

// Runner is an in-process execx.Runner that fakes systemctl and launchctl.
// It logs each call, the files then in Dir, and the call's env. A call
// whose arguments include a key of Fail prints its value and fails; one
// whose arguments include a key of Out prints its value. With Missing set,
// every command is not found.
//
// It models a user manager that loads units from UnitPath, and one service,
// which enable --now and restart start, and disable and stop stop. It
// models one launchd agent, which bootstrap loads, and bootout unloads after
// Lingering more prints; print fails while the agent is not loaded.
type Runner struct {
	Dir       string
	Fail      map[string]string
	Out       map[string]string
	Missing   bool
	UnitPath  string
	Active    bool
	Loaded    bool
	Lingering int

	mu        sync.Mutex
	calls     []string
	files     [][]string
	envs      []map[string]string
	unloading int
}

func (r *Runner) Run(_ context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	entries, _ := os.ReadDir(r.Dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	r.files = append(r.files, names)
	r.envs = append(r.envs, env)
	if r.Missing {
		return nil, nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	for _, a := range args {
		if msg, ok := r.Fail[a]; ok {
			return nil, []byte(msg + "\n"), errors.New("exit status 1")
		}
	}
	for _, a := range args {
		if out, ok := r.Out[a]; ok {
			return []byte(out + "\n"), nil, nil
		}
	}
	switch name {
	case "systemctl":
		return r.systemctl(args[1:])
	case "launchctl":
		return r.launchctl(args)
	}
	return nil, nil, nil
}

func (r *Runner) systemctl(args []string) (stdout, stderr []byte, err error) {
	switch args[0] {
	case "show":
		if slices.Contains(args, "UnitPath") {
			return []byte(r.UnitPath + "\n"), nil, nil
		}
		if r.Active {
			return []byte("active\n"), nil, nil
		}
		return []byte("inactive\n"), nil, nil
	case "enable":
		r.Active = r.Active || args[1] == "--now"
	case "restart":
		r.Active = true
	case "disable", "stop":
		r.Active = false
	}
	return nil, nil, nil
}

func (r *Runner) launchctl(args []string) (stdout, stderr []byte, err error) {
	switch args[0] {
	case "bootstrap":
		r.Loaded = true
	case "bootout":
		r.Loaded, r.unloading = false, r.Lingering
	case "print":
		if r.unloading > 0 {
			r.unloading--
			return nil, nil, nil
		}
		if !r.Loaded {
			return nil, []byte("Could not find service\n"), errors.New("exit status 113")
		}
	}
	return nil, nil, nil
}

// Calls gives each call's command and arguments, joined by spaces.
func (r *Runner) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// Files gives, for each call, the names of the files then in Dir.
func (r *Runner) Files() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.files)
}

// Envs gives each call's env.
func (r *Runner) Envs() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.envs)
}

// Reset forgets the calls so far.
func (r *Runner) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls, r.files, r.envs = nil, nil, nil
}
