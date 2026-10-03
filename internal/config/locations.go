// Package config locates and loads lg's store and config file.
package config

import "path/filepath"

type Roots struct {
	Home       string
	ConfigFile string
}

// Locations resolves lg's roots from env, where an empty value counts as unset.
func Locations(env map[string]string) (Roots, error) {
	return Roots{
		Home: firstSet(env["LG_HOME"],
			under(env["XDG_DATA_HOME"], "lg"),
			under(env["HOME"], ".local", "share", "lg")),
		ConfigFile: firstSet(env["LG_CONFIG"],
			under(env["XDG_CONFIG_HOME"], "lg", "config.yaml"),
			under(env["HOME"], ".config", "lg", "config.yaml")),
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
