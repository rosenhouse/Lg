package service_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/service"
)

// units are the env cases of the golden files under testdata.
var units = map[string]service.Unit{
	"minimal": {
		Name: "lg",
		Exe:  "/opt/lg/bin/lg",
		Env:  map[string]string{"LG_GH": "/opt/gh/bin/gh"},
		Log:  "/Users/u/.local/share/lg/state/daemon.log",
	},
	"full": {
		Name: "lg",
		Exe:  "/opt/lg/bin/lg",
		Env: map[string]string{
			"LG_GH":         "/opt/gh/bin/gh",
			"LG_HOME":       "/srv/lg",
			"LG_CONFIG":     "/etc/lg/config.yaml",
			"HTTPS_PROXY":   "http://proxy.example:3128",
			"https_proxy":   "http://proxy.example:3128",
			"HTTP_PROXY":    "http://proxy.example:3128",
			"NO_PROXY":      "localhost,.example",
			"SSL_CERT_FILE": "/etc/ssl/ca.pem",
		},
		Log: "/srv/lg/state/daemon.log",
	},
	"quoting": {
		Name: "lg-test.1",
		Exe:  "/Users/a b/100% $HOME & <bin>/lg",
		Env:  map[string]string{"LG_GH": "/opt/gh/bin/gh", "HTTPS_PROXY": `http://u:p%40$x"\@proxy:1`},
		Log:  "/Users/a b/<lg> & state/daemon.log",
	},
}

var _ = DescribeTable("RenderSystemd", Label("install"),
	func(name string) {
		Expect(service.RenderSystemd(units[name])).To(Equal(golden(name + ".service")))
	},
	Entry(nil, "minimal"),
	Entry(nil, "full"),
	Entry(nil, "quoting"),
)

var _ = DescribeTable("RenderLaunchd", Label("install"),
	func(name string) {
		Expect(service.RenderLaunchd(units[name])).To(Equal(golden(name + ".plist")))
	},
	Entry(nil, "minimal"),
	Entry(nil, "full"),
	Entry(nil, "quoting"),
)

func golden(name string) []byte {
	GinkgoHelper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	Expect(err).NotTo(HaveOccurred())
	return data
}

var _ = Describe("RenderSystemd", Label("install"), func() {
	DescribeTable("refuses an lg path that systemd cannot run",
		func(exe string) {
			_, err := service.RenderSystemd(service.Unit{Name: "lg", Exe: exe, Env: units["minimal"].Env})
			Expect(err).To(MatchError(ContainSubstring("quote or backslash")))
		},
		Entry(nil, `/opt/"lg"/lg`),
		Entry(nil, `/opt/lg's/lg`),
		Entry(nil, `/opt/l\g/lg`),
	)
})

var _ = DescribeTable("a unit with a control character", Label("install"),
	func(render func(service.Unit) ([]byte, error), u service.Unit) {
		_, err := render(u)
		Expect(err).To(MatchError(ContainSubstring("control character")))
	},
	Entry("in Exe for systemd", service.RenderSystemd, service.Unit{Name: "lg", Exe: "/opt/lg\n/lg"}),
	Entry("in an env value for systemd", service.RenderSystemd, service.Unit{Name: "lg", Exe: "/lg", Env: map[string]string{"LG_HOME": "/a\tb"}}),
	Entry("in an env name for launchd", service.RenderLaunchd, service.Unit{Name: "lg", Exe: "/lg", Env: map[string]string{"A\x7f": "b"}}),
	Entry("in Log for launchd", service.RenderLaunchd, service.Unit{Name: "lg", Exe: "/lg", Log: "/a\x00"}),
	Entry("in Name for launchd", service.RenderLaunchd, service.Unit{Name: "l\rg", Exe: "/lg"}),
)
