package harness_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("Scrub", Label("cli"), func() {
	It("drops lg, XDG, GitHub, HOME, Claude config, ripgrep config, proxy and CA bundle variables", func() {
		Expect(harness.Scrub([]string{
			"LG_HOME=/lg", "XDG_DATA_HOME=/x", "GH_TOKEN=t", "GITHUB_TOKEN=t", "HOME=/h", "CLAUDE_CONFIG_DIR=/c", "RIPGREP_CONFIG_PATH=/rc", "SSL_CERT_FILE=/ca",
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

var _ = Describe("Env PATH helpers", Label("install"), func() {
	It("puts a dir first with PrependPath, and drops every dir that holds a name with PathWithout", func() {
		env := harness.New("/no/lg")
		with, without := GinkgoT().TempDir(), GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(with, "gh"), nil, 0o755)).To(Succeed())
		env.Setenv("PATH", with+":"+without+":"+with)

		env.PrependPath("/first")
		Expect(env.Getenv("PATH")).To(Equal("/first:" + with + ":" + without + ":" + with))

		env.PathWithout("gh")
		Expect(env.Getenv("PATH")).To(Equal("/first:" + without))
	})
})
