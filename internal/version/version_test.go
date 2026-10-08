package version_test

import (
	"runtime/debug"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/version"
)

var _ = DescribeTable("FromBuildInfo", Label("version"),
	func(info *debug.BuildInfo, want string) {
		Expect(version.FromBuildInfo(info, info != nil)).To(Equal(want))
	},
	Entry("gives the module version of go install …@version", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}}, "v0.3.0"),
	Entry("gives the pseudo-version that go build stamps from git",
		&debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261007232459-e3d572fc0782+dirty"}}, "v0.0.0-20261007232459-e3d572fc0782+dirty"),
	Entry("gives dev for a build without VCS info", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"),
	Entry("gives dev without a module version", &debug.BuildInfo{}, "dev"),
	Entry("gives dev without build info", nil, "dev"),
)
