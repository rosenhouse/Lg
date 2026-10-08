// Package config locates lg's store, config file and skill, and loads the config file.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Error is a problem with lg's configuration or the environment that locates it.
type Error string

func (e Error) Error() string { return string(e) }

type Roots struct {
	Store string
	Data  string
	State string
	Tmp   string
}

// Locations resolves the store as LG_HOME > XDG_DATA_HOME/lg > HOME/.local/share/lg.
func Locations(env map[string]string) (Roots, error) {
	store, err := resolve(env, "the store",
		source{name: "LG_HOME"},
		source{name: "XDG_DATA_HOME", elems: []string{"lg"}, xdg: true},
		source{name: "HOME", elems: []string{".local", "share", "lg"}})
	if err != nil {
		return Roots{}, err
	}
	// Only LG_HOME can put the store at the root, where tmp/ would be /tmp.
	if filepath.Dir(store) == store {
		return Roots{}, Error(fmt.Sprintf("LG_HOME must not be the filesystem root: %q", env["LG_HOME"]))
	}
	return Roots{
		Store: store,
		Data:  filepath.Join(store, "data"),
		State: filepath.Join(store, "state"),
		Tmp:   filepath.Join(store, "tmp"),
	}, nil
}

// File resolves the config file as LG_CONFIG > XDG_CONFIG_HOME/lg/config.yaml > HOME/.config/lg/config.yaml.
func File(env map[string]string) (string, error) {
	return resolve(env, "config.yaml",
		source{name: "LG_CONFIG"},
		source{name: "XDG_CONFIG_HOME", elems: []string{"lg", "config.yaml"}, xdg: true},
		source{name: "HOME", elems: []string{".config", "lg", "config.yaml"}})
}

// source is an env var and the path under it. The XDG spec says to ignore a
// relative XDG path; any other relative path is an error.
type source struct {
	name  string
	elems []string
	xdg   bool
}

// resolve joins the first set source with its elems. Empty values count as unset.
func resolve(env map[string]string, what string, sources ...source) (string, error) {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.name
		if s.xdg {
			names[i] = "an absolute " + s.name
		}
		dir := env[s.name]
		if dir == "" || (s.xdg && !filepath.IsAbs(dir)) {
			continue
		}
		if !filepath.IsAbs(dir) {
			return "", Error(fmt.Sprintf("%s must be an absolute path: %q", s.name, dir))
		}
		return filepath.Join(append([]string{dir}, s.elems...)...), nil
	}
	last := len(names) - 1
	return "", Error(fmt.Sprintf("cannot locate %s: set %s or %s", what, strings.Join(names[:last], ", "), names[last]))
}

// SkillFile resolves lg's skill as CLAUDE_CONFIG_DIR/skills/lg/SKILL.md > HOME/.claude/skills/lg/SKILL.md.
func SkillFile(env map[string]string) (string, error) {
	return resolve(env, "SKILL.md",
		source{name: "CLAUDE_CONFIG_DIR", elems: []string{"skills", "lg", "SKILL.md"}},
		source{name: "HOME", elems: []string{".claude", "skills", "lg", "SKILL.md"}})
}
