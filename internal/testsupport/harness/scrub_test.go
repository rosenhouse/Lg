package harness_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("Scrub", Label("cli"), func() {
	It("drops lg, XDG, GitHub, HOME and proxy variables", func() {
		Expect(harness.Scrub([]string{
			"LG_HOME=/lg", "XDG_DATA_HOME=/x", "GH_TOKEN=t", "GITHUB_TOKEN=t", "HOME=/h",
			"HTTPS_PROXY=p", "https_proxy=p", "HTTP_PROXY=p", "http_proxy=p",
			"NO_PROXY=n", "no_proxy=n", "ALL_PROXY=p", "all_proxy=p",
			"LANG=C", "PATH=/usr/bin",
		}, "/bin/lg")).To(Equal(map[string]string{"LANG": "C", "PATH": "/bin/lg:/usr/bin"}))
	})

	It("sets PATH to the lg dir when the environment has none", func() {
		Expect(harness.Scrub([]string{"LANG=C"}, "/bin/lg")).To(Equal(map[string]string{"LANG": "C", "PATH": "/bin/lg"}))
	})
})
