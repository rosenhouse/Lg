// Package version gives lg's version.
package version

import "runtime/debug"

// Version is set with -ldflags -X, else is the module version that go build
// stamps, else dev.
var Version string

func init() {
	if Version == "" {
		Version = FromBuildInfo(debug.ReadBuildInfo())
	}
}

// FromBuildInfo gives the module version, else dev.
func FromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
