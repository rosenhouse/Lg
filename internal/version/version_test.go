package version_test

import (
	"runtime/debug"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/version"
)

func vcs(revision, modified string) []debug.BuildSetting {
	return []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: modified}}
}

var _ = DescribeTable("FromBuildInfo", Label("version"),
	func(info *debug.BuildInfo, want string) {
		Expect(version.FromBuildInfo(info, info != nil)).To(Equal(want))
	},
	Entry("gives the module version of go install …@version",
		&debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}, Settings: vcs("4064cb024fbb0123", "false")}, "v0.3.0"),
	Entry("gives the VCS revision, cut to 12, without a module version",
		&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("4064cb024fbb0123", "false")}, "4064cb024fbb"),
	Entry("marks a revision built from a modified tree",
		&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("4064cb024fbb0123", "true")}, "4064cb024fbb-dirty"),
	Entry("gives dev without a module version or revision", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"),
	Entry("gives dev without build info", nil, "dev"),
)
