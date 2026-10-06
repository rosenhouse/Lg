package service_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/service"
)

// units are the env cases of the golden files under testdata. Of these,
// minimal is from an install with LG_GH unset, and full with LG_GH set.
var units = map[string]service.Unit{
	"minimal": {
		Name: "lg",
		Exe:  "/opt/lg/bin/lg",
		Env:  map[string]string{"PATH": "/opt/gh/bin:/usr/local/bin:/usr/bin:/bin"},
		Log:  "/Users/u/.local/share/lg/state/daemon.log",
	},
	"full": {
		Name: "lg",
		Exe:  "/opt/lg/bin/lg",
		Env: map[string]string{
			"PATH":          "/opt/gh/bin:/usr/local/bin:/usr/bin:/bin",
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

var _ = DescribeTable("a unit with a control character names where, never the value", Label("install"),
	func(render func(service.Unit) ([]byte, error), u service.Unit, where string) {
		_, err := render(u)
		Expect(err).To(MatchError(where + " contains a control character, which a service cannot carry"))
	},
	Entry("in Exe for systemd", service.RenderSystemd, service.Unit{Name: "lg", Exe: "/opt/lg\n/lg"}, "the path of lg"),
	Entry("in an env value for systemd", service.RenderSystemd, service.Unit{Name: "lg", Exe: "/lg", Env: map[string]string{"HTTPS_PROXY": "http://u:secret\tpw@p"}}, "HTTPS_PROXY"),
	Entry("in an env name for launchd", service.RenderLaunchd, service.Unit{Name: "lg", Exe: "/lg", Env: map[string]string{"A\x7f": "b"}}, "a variable name"),
	Entry("in Log for launchd", service.RenderLaunchd, service.Unit{Name: "lg", Exe: "/lg", Log: "/a\x00"}, "the log path"),
	Entry("in Name for launchd", service.RenderLaunchd, service.Unit{Name: "l\rg", Exe: "/lg"}, "the service name"),
)

var _ = DescribeTable("a unit with invalid UTF-8 names where, never the value", Label("install"),
	func(render func(service.Unit) ([]byte, error), u service.Unit, where string) {
		_, err := render(u)
		Expect(err).To(MatchError(where + " is not valid UTF-8, which a service cannot carry"))
	},
	Entry("in an env value for systemd", service.RenderSystemd, service.Unit{Name: "lg", Exe: "/lg", Env: map[string]string{"LG_HOME": "/caf\xe9"}}, "LG_HOME"),
	Entry("in Exe for launchd", service.RenderLaunchd, service.Unit{Name: "lg", Exe: "/caf\xe9/lg"}, "the path of lg"),
)
