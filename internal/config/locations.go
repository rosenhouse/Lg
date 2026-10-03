// Package config locates and loads lg's store and config file.
package config

import "path/filepath"

type Roots struct {
	Home       string
	ConfigFile string
}

// Locations resolves lg's roots from env, where an empty value counts as unset.
func Locations(env map[string]string) (Roots, error) {
	switch {
	case env["LG_HOME"] != "":
		return Roots{Home: env["LG_HOME"]}, nil
	case env["XDG_DATA_HOME"] != "":
		return Roots{Home: filepath.Join(env["XDG_DATA_HOME"], "lg")}, nil
	default:
		return Roots{Home: filepath.Join(env["HOME"], ".local", "share", "lg")}, nil
	}
}
