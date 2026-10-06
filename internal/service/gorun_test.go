package service_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/service"
)

var _ = DescribeTable("BuiltByGoRun", Label("install"),
	func(exe string, want bool) {
		Expect(service.BuiltByGoRun(exe)).To(Equal(want))
	},
	Entry("rejects a binary under a go-build dir in os.TempDir", filepath.Join(os.TempDir(), "go-build3021984711", "b001", "exe", "lg"), true),
	Entry("rejects a binary under a go-build dir in GOTMPDIR", "/home/u/gotmp/go-build17/b001/exe/lg", true),
	Entry("rejects a binary go run cached in ~/.cache/go-build", "/home/u/.cache/go-build/4b/4b9e5baa00000000000000000000000000000000000000000000000000000000-d/lg", true),
	Entry("rejects a binary go run cached in ~/Library/Caches/go-build", "/Users/u/Library/Caches/go-build/4b/4b9e5baa00000000000000000000000000000000000000000000000000000000-d/lg", true),
	Entry("rejects a binary go run cached in a custom GOCACHE", "/x/gocache/4b/4b9e5baa00000000000000000000000000000000000000000000000000000000-d/lg", true),
	Entry("accepts the gexec build dir", filepath.Join(os.TempDir(), "gexec_artifacts1873452", "lg"), false),
	Entry("accepts an installed binary", "/home/u/go/bin/lg", false),
	Entry("accepts a dir that only starts like go-build", "/home/u/go-build-tools/bin/lg", false),
)
