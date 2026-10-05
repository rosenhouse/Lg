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

var _ = Describe("ScrubLive", Label("cli"), func() {
	It("keeps the proxy variables, SSL_CERT_FILE, GH_TOKEN, GITHUB_TOKEN, GH_CONFIG_DIR and HOME, and drops the other variables Scrub drops", func() {
		Expect(harness.ScrubLive([]string{
			"LG_HOME=/lg", "LG_TEST_NOW=2026-10-03T18:00:00Z", "XDG_DATA_HOME=/x", "GH_HOST=h", "GITHUB_API_URL=u", "ALL_PROXY=p",
			"HTTPS_PROXY=P", "https_proxy=p", "HTTP_PROXY=P", "http_proxy=p", "NO_PROXY=N", "no_proxy=n",
			"SSL_CERT_FILE=/ca", "GH_TOKEN=t", "GITHUB_TOKEN=T", "GH_CONFIG_DIR=/gh", "HOME=/h",
			"LANG=C", "PATH=/usr/bin",
		}, "/bin/lg")).To(Equal(map[string]string{
			"HTTPS_PROXY": "P", "https_proxy": "p", "HTTP_PROXY": "P", "http_proxy": "p", "NO_PROXY": "N", "no_proxy": "n",
			"SSL_CERT_FILE": "/ca", "GH_TOKEN": "t", "GITHUB_TOKEN": "T", "GH_CONFIG_DIR": "/gh", "HOME": "/h",
			"LANG": "C", "PATH": "/bin/lg:/usr/bin",
		}))
	})
})
