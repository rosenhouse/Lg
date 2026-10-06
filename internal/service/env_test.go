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
	It("bakes in PATH, LG_GH as gh when LG_GH is set, and only those of LG_HOME, LG_CONFIG, XDG_DATA_HOME, XDG_CONFIG_HOME, GH_CONFIG_DIR, SSL_CERT_FILE and the upper- or lowercase proxy variables that are set", func() {
		Expect(service.Env("linux", map[string]string{
			"LG_GH":           "gh",
			"LG_HOME":         "/srv/lg",
			"LG_CONFIG":       "",
			"XDG_DATA_HOME":   "/xdg/data",
			"XDG_CONFIG_HOME": "/xdg/config",
			"XDG_STATE_HOME":  "/xdg/state",
			"SSL_CERT_FILE":   "/ca.pem",
			"HTTPS_PROXY":     "http://p:1",
			"https_proxy":     "http://p:2",
			"http_proxy":      "http://p:3",
			"No_Proxy":        "mixed",
			"no_proxy":        "localhost",
			"ALL_PROXY":       "socks5://p",
			"GH_TOKEN":        "gho_1",
			"GITHUB_TOKEN":    "gho_2",
			"GH_CONFIG_DIR":   "/gh",
			"LG_TEST_NOW":     "2026-10-03T18:00:00Z",
			"HOME":            "/home/u",
			"PATH":            "/home/u/bin:/usr/bin",
		}, "/opt/gh/bin/gh")).To(Equal(map[string]string{
			"PATH":            "/opt/gh/bin:/usr/local/bin:/usr/bin:/bin",
			"LG_GH":           "/opt/gh/bin/gh",
			"LG_HOME":         "/srv/lg",
			"XDG_DATA_HOME":   "/xdg/data",
			"XDG_CONFIG_HOME": "/xdg/config",
			"GH_CONFIG_DIR":   "/gh",
			"SSL_CERT_FILE":   "/ca.pem",
			"HTTPS_PROXY":     "http://p:1",
			"https_proxy":     "http://p:2",
			"http_proxy":      "http://p:3",
			"no_proxy":        "localhost",
		}))
	})

	DescribeTable("bakes in no LG_GH unless LG_GH is set, since a set LG_GH permits a loopback api_url",
		func(env map[string]string) {
			Expect(service.Env("linux", env, "/opt/gh/bin/gh")).NotTo(HaveKey("LG_GH"))
		},
		Entry("when LG_GH is unset", map[string]string{"PATH": "/opt/gh/bin"}),
		Entry("when LG_GH is empty", map[string]string{"LG_GH": "", "PATH": "/opt/gh/bin"}),
	)

	DescribeTable("bakes in PATH as gh's dir, then the platform's system dirs without repeats",
		func(goos, gh, path string) {
			Expect(service.Env(goos, nil, gh)).To(HaveKeyWithValue("PATH", path))
		},
		Entry(nil, "linux", "/opt/gh/bin/gh", "/opt/gh/bin:/usr/local/bin:/usr/bin:/bin"),
		Entry(nil, "linux", "/usr/bin/gh", "/usr/bin:/usr/local/bin:/bin"),
		Entry(nil, "darwin", "/opt/gh/bin/gh", "/opt/gh/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"),
		Entry(nil, "darwin", "/opt/homebrew/bin/gh", "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"),
	)

	It("makes a relative SSL_CERT_FILE or GH_CONFIG_DIR absolute, and drops a relative XDG dir, since a service runs elsewhere", func() {
		wd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())

		Expect(service.Env("linux", map[string]string{
			"SSL_CERT_FILE":   "ca.pem",
			"GH_CONFIG_DIR":   "ghconf",
			"XDG_DATA_HOME":   "rel",
			"XDG_CONFIG_HOME": "relc",
		}, "/opt/gh/bin/gh")).To(Equal(map[string]string{
			"PATH":          "/opt/gh/bin:/usr/local/bin:/usr/bin:/bin",
			"SSL_CERT_FILE": filepath.Join(wd, "ca.pem"),
			"GH_CONFIG_DIR": filepath.Join(wd, "ghconf"),
		}))
	})
})

var _ = Describe("Executable", Label("install"), func() {
	var dir, self, link string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		self = filepath.Join(dir, "Cellar", "lg")
		Expect(os.MkdirAll(filepath.Dir(self), 0o755)).To(Succeed())
		Expect(os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
		link = filepath.Join(dir, "bin", "lg")
		Expect(os.MkdirAll(filepath.Dir(link), 0o755)).To(Succeed())
		Expect(os.Symlink(self, link)).To(Succeed())
	})

	It("keeps the symlink lg was run by", func() {
		Expect(service.Executable(link, "", self)).To(Equal(link))
	})

	It("looks a bare name up on PATH", func() {
		Expect(service.Executable("lg", "/nowhere:"+filepath.Dir(link), self)).To(Equal(link))
	})

	It("makes a relative path absolute", func() {
		wd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		rel, err := filepath.Rel(wd, link)
		Expect(err).NotTo(HaveOccurred())

		Expect(service.Executable(rel, "", self)).To(Equal(link))
	})

	DescribeTable("falls back to the running executable",
		func(arg0 func() string) {
			Expect(service.Executable(arg0(), "/nowhere:"+filepath.Dir(link), self)).To(Equal(self))
		},
		Entry("when the name is not on PATH", func() string { return "lg-other" }),
		Entry("when the path names no file", func() string { return filepath.Join(dir, "missing") }),
		Entry("when the path names another file", func() string {
			other := filepath.Join(dir, "other")
			Expect(os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
			return other
		}),
	)
})
