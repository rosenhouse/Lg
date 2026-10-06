package service_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/service"
)

var _ = Describe("FindGH", Label("install"), func() {
	var dir, gh string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		gh = filepath.Join(dir, "gh")
		Expect(os.WriteFile(gh, []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
	})

	It("gives the executable LG_GH names", func() {
		Expect(service.FindGH(map[string]string{"LG_GH": gh, "PATH": "/nowhere"})).To(Equal(gh))
	})

	It("looks a bare LG_GH up on PATH", func() {
		Expect(os.Rename(gh, filepath.Join(dir, "my-gh"))).To(Succeed())

		Expect(service.FindGH(map[string]string{"LG_GH": "my-gh", "PATH": "/nowhere:" + dir})).To(Equal(filepath.Join(dir, "my-gh")))
	})

	It("looks gh up on PATH when LG_GH is unset or empty", func() {
		Expect(service.FindGH(map[string]string{"LG_GH": "", "PATH": "/nowhere:" + dir})).To(Equal(gh))
	})

	It("makes a relative LG_GH absolute", func() {
		wd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		rel, err := filepath.Rel(wd, gh)
		Expect(err).NotTo(HaveOccurred())

		Expect(service.FindGH(map[string]string{"LG_GH": rel})).To(Equal(gh))
	})

	DescribeTable("refuses when gh is neither at LG_GH nor on PATH",
		func(env func() map[string]string) {
			_, err := service.FindGH(env())
			Expect(err).To(MatchError(ContainSubstring("gh is neither at LG_GH nor on PATH")))
		},
		Entry("with LG_GH unset and gh not on PATH", func() map[string]string { return map[string]string{"PATH": "/nowhere"} }),
		Entry("with LG_GH naming no file, even when gh is on PATH", func() map[string]string {
			return map[string]string{"LG_GH": filepath.Join(dir, "missing"), "PATH": dir}
		}),
		Entry("with LG_GH naming a file that is not executable", func() map[string]string {
			Expect(os.Chmod(gh, 0o644)).To(Succeed())
			return map[string]string{"LG_GH": gh}
		}),
		Entry("with gh only in a relative PATH dir", func() map[string]string {
			wd, err := os.Getwd()
			Expect(err).NotTo(HaveOccurred())
			rel, err := filepath.Rel(wd, dir)
			Expect(err).NotTo(HaveOccurred())
			return map[string]string{"PATH": rel}
		}),
		Entry("with LG_GH naming a dir", func() map[string]string { return map[string]string{"LG_GH": dir} }),
	)
})

var _ = Describe("Env", Label("install"), func() {
	It("bakes in LG_GH as gh, and only those of LG_HOME, LG_CONFIG, SSL_CERT_FILE and the upper- or lowercase proxy variables that are set", func() {
		Expect(service.Env(map[string]string{
			"LG_GH":         "gh",
			"LG_HOME":       "/srv/lg",
			"LG_CONFIG":     "",
			"SSL_CERT_FILE": "/ca.pem",
			"HTTPS_PROXY":   "http://p:1",
			"https_proxy":   "http://p:2",
			"http_proxy":    "http://p:3",
			"No_Proxy":      "mixed",
			"no_proxy":      "localhost",
			"ALL_PROXY":     "socks5://p",
			"GH_TOKEN":      "gho_1",
			"GITHUB_TOKEN":  "gho_2",
			"GH_CONFIG_DIR": "/gh",
			"LG_TEST_NOW":   "2026-10-03T18:00:00Z",
			"HOME":          "/home/u",
			"PATH":          "/usr/bin",
		}, "/opt/gh")).To(Equal(map[string]string{
			"LG_GH":         "/opt/gh",
			"LG_HOME":       "/srv/lg",
			"SSL_CERT_FILE": "/ca.pem",
			"HTTPS_PROXY":   "http://p:1",
			"https_proxy":   "http://p:2",
			"http_proxy":    "http://p:3",
			"no_proxy":      "localhost",
		}))
	})
})
