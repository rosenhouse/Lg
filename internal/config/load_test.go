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
