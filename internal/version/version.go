// Package version gives lg's version.
package version

import "runtime/debug"

// Version is set with -ldflags -X, else from the build info.
var Version string

func init() {
	if Version == "" {
		Version = FromBuildInfo(debug.ReadBuildInfo())
	}
}

// FromBuildInfo gives the module version, else the VCS revision with -dirty
// for a modified tree, else dev.
func FromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	revision := settings["vcs.revision"]
	if revision == "" {
		return "dev"
	}
	revision = revision[:min(12, len(revision))]
	if settings["vcs.modified"] == "true" {
		revision += "-dirty"
	}
	return revision
}
