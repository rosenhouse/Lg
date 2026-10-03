package version_test

import (
	"runtime/debug"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/version"
)

var _ = Describe("FromBuildInfo", Label("cli"), func() {
	module := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }

	DescribeTable("prefers the stamped version, then the module version",
		func(stamped string, info *debug.BuildInfo, want string) {
			Expect(version.FromBuildInfo(stamped, info)).To(Equal(want))
		},
		Entry("a stamped version wins", "v1.0.0", module("v2.0.0"), "v1.0.0"),
		Entry("an unstamped build takes the module version", "dev", module("v0.0.0-20261003210112-7e7e335d9b76"), "v0.0.0-20261003210112-7e7e335d9b76"),
		Entry("a (devel) module stays dev", "dev", module("(devel)"), "dev"),
		Entry("missing build info stays dev", "dev", nil, "dev"),
	)
})
