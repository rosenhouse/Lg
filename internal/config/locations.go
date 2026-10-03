// Package config locates and loads lg's store and config file.
package config

import (
	"errors"
	"path/filepath"
)

type Roots struct {
	Home       string
	Data       string
	State      string
	Tmp        string
	ConfigFile string
}

// Locations resolves lg's roots from env, where an empty value counts as unset.
func Locations(env map[string]string) (Roots, error) {
	home := firstSet(env["LG_HOME"],
		under(env["XDG_DATA_HOME"], "lg"),
		under(env["HOME"], ".local", "share", "lg"))
	configFile := firstSet(env["LG_CONFIG"],
		under(env["XDG_CONFIG_HOME"], "lg", "config.yaml"),
		under(env["HOME"], ".config", "lg", "config.yaml"))
	if home == "" || configFile == "" {
		return Roots{}, errors.New("HOME is not set")
	}
	return Roots{
		Home:       home,
		Data:       filepath.Join(home, "data"),
		State:      filepath.Join(home, "state"),
		Tmp:        filepath.Join(home, "tmp"),
		ConfigFile: configFile,
	}, nil
}

// under joins dir and elems, or returns "" when dir is unset.
func under(dir string, elems ...string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(append([]string{dir}, elems...)...)
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
