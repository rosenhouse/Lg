// Package version reports lg's version.
package version

import "runtime/debug"

// Version is stamped with -ldflags.
var Version = "dev"

// Get returns Version, or the module version when Version is unstamped.
func Get() string {
	info, _ := debug.ReadBuildInfo()
	return FromBuildInfo(Version, info)
}

// FromBuildInfo returns stamped unless it is "dev", in which case it returns
// info's module version, if that is known.
func FromBuildInfo(stamped string, info *debug.BuildInfo) string {
	if stamped != "dev" || info == nil || info.Main.Version == "(devel)" {
		return stamped
	}
	return info.Main.Version
}
