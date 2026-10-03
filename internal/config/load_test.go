package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/config"
)

var _ = Describe("Load", Label("sync"), func() {
	write := func(yaml string) string {
		path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(path, []byte(yaml), 0o600)).To(Succeed())
		return path
	}

	It("reads host, repo and api_url", func() {
		cfg, err := config.Load(write("host: ghe.corp.example\nrepo: platform/infra\napi_url: http://127.0.0.1:1/api/v3\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).To(Equal(config.Config{Host: "ghe.corp.example", Repo: "platform/infra", APIURL: "http://127.0.0.1:1/api/v3"}))
	})

	It("defaults host to github.com", func() {
		Expect(config.Load(write("repo: rosenhouse/lg\n"))).To(Equal(config.Config{Host: "github.com", Repo: "rosenhouse/lg"}))
	})

	DescribeTable("rejects a repo that is not owner/name",
		func(repo string) {
			_, err := config.Load(write("repo: '" + repo + "'\n"))
			Expect(err).To(MatchError(config.Error(`repo must be owner/name: "` + repo + `"`)))
		},
		Entry("empty", ""),
		Entry("no owner", "lg"),
		Entry("empty owner", "/lg"),
		Entry("empty name", "rosenhouse/"),
		Entry("three parts", "a/b/c"),
		Entry("a space", "rosen house/lg"),
	)

	It("lowercases host", func() {
		cfg, err := config.Load(write("host: GitHub.com\nrepo: rosenhouse/lg\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Host).To(Equal("github.com"))
	})

	DescribeTable("rejects a host that is not a host name",
		func(host string) {
			_, err := config.Load(write("host: '" + host + "'\nrepo: rosenhouse/lg\n"))
			Expect(err).To(MatchError(config.Error(`host must be a host name: "` + host + `"`)))
		},
		Entry("empty", ""),
		Entry("a scheme", "https://github.com"),
		Entry("a path", "github.com/api"),
		Entry("a parent dir", ".."),
	)

	DescribeTable("rejects an api_url that is not an absolute http or https URL",
		func(apiURL string) {
			_, err := config.Load(write("repo: rosenhouse/lg\napi_url: '" + apiURL + "'\n"))
			Expect(err).To(MatchError(config.Error(`api_url must be an http or https URL: "` + apiURL + `"`)))
		},
		Entry("no scheme", "127.0.0.1:18301"),
		Entry("another scheme", "ftp://127.0.0.1"),
		Entry("no host", "http://"),
	)

	It("rejects an unknown key", func() {
		path := write("repo: rosenhouse/lg\napi-url: http://127.0.0.1:1\n")
		_, err := config.Load(path)
		Expect(err).To(BeAssignableToTypeOf(config.Error("")))
		Expect(err).To(MatchError(And(HavePrefix(path+": "), ContainSubstring("api-url"))))
	})

	It("returns a config.Error for a missing file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "missing.yaml")
		_, err := config.Load(path)
		Expect(err).To(BeAssignableToTypeOf(config.Error("")))
		Expect(err).To(MatchError(ContainSubstring(path)))
	})

	It("returns a config.Error naming the file for invalid YAML", func() {
		path := write("repo: [\n")
		_, err := config.Load(path)
		Expect(err).To(BeAssignableToTypeOf(config.Error("")))
		Expect(err).To(MatchError(HavePrefix(path + ": ")))
	})
})
