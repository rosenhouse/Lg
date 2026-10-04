package config_test

import (
	"math"
	"os"
	"path/filepath"
	"time"

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
		Expect(cfg).To(Equal(config.Config{Host: "ghe.corp.example", Repo: "platform/infra", APIURL: "http://127.0.0.1:1/api/v3", LogGrace: config.Duration(time.Hour), ArtifactMaxBytes: 500_000_000}))
	})

	It("defaults host to github.com", func() {
		Expect(config.Load(write("repo: rosenhouse/lg\n"))).To(HaveField("Host", "github.com"))
	})

	It("defaults log_grace to 1h", Label("failures"), func() {
		Expect(config.Load(write("repo: rosenhouse/lg\n"))).To(HaveField("LogGrace", config.Duration(time.Hour)))
	})

	It("reads log_grace as a Go duration", Label("failures"), func() {
		Expect(config.Load(write("repo: rosenhouse/lg\nlog_grace: 90m\n"))).To(HaveField("LogGrace", config.Duration(90*time.Minute)))
	})

	DescribeTable("accepts a log_grace of 0",
		func(zero string) {
			Expect(config.Load(write("repo: rosenhouse/lg\nlog_grace: " + zero + "\n"))).To(HaveField("LogGrace", config.Duration(0)))
		},
		Entry("with a unit", "0s", Label("failures")),
		Entry("bare", "0", Label("failures")),
	)

	DescribeTable("rejects a log_grace that is not a duration",
		func(value, message string) {
			_, err := config.Load(write("repo: rosenhouse/lg\nlog_grace: " + value + "\n"))
			Expect(err).To(MatchError(HaveSuffix("config.yaml: line 2: " + message)))
			Expect(err).To(BeAssignableToTypeOf(config.Error("")))
		},
		Entry("with no unit", "3600", `time: missing unit in duration "3600"`, Label("failures")),
		Entry("out of range", "3000000h", `time: invalid duration "3000000h"`, Label("failures")),
		Entry("a sequence", "[1h]", "want a duration such as 1h, not !!seq", Label("failures")),
		Entry("a mapping", "{a: 1}", "want a duration such as 1h, not !!map", Label("failures")),
	)

	It("rejects a negative log_grace", Label("failures"), func() {
		_, err := config.Load(write("repo: rosenhouse/lg\nlog_grace: -1m\n"))
		Expect(err).To(MatchError(config.Error(`log_grace must not be negative: "-1m0s"`)))
	})

	It("defaults artifact_max_bytes to 500MB", Label("artifacts"), func() {
		Expect(config.Load(write("repo: rosenhouse/lg\n"))).To(HaveField("ArtifactMaxBytes", config.Bytes(500_000_000)))
	})

	DescribeTable("reads artifact_max_bytes with MB and GB decimal, and MiB and GiB binary", Label("artifacts"),
		func(value string, bytes config.Bytes) {
			Expect(config.Load(write("repo: rosenhouse/lg\nartifact_max_bytes: " + value + "\n"))).To(HaveField("ArtifactMaxBytes", bytes))
		},
		Entry("one byte", "1", config.Bytes(1)),
		Entry("bare", "700", config.Bytes(700)),
		Entry("B", "700B", config.Bytes(700)),
		Entry("KB", "2KB", config.Bytes(2_000)),
		Entry("MB", "500MB", config.Bytes(500_000_000)),
		Entry("GB", "2GB", config.Bytes(2_000_000_000)),
		Entry("TB", "1TB", config.Bytes(1_000_000_000_000)),
		Entry("KiB", "2KiB", config.Bytes(2<<10)),
		Entry("MiB", "1MiB", config.Bytes(1<<20)),
		Entry("GiB", "1GiB", config.Bytes(1<<30)),
		Entry("TiB", "1TiB", config.Bytes(1<<40)),
		Entry("the largest", "9223372036854775807", config.Bytes(math.MaxInt64)),
	)

	DescribeTable("rejects an artifact_max_bytes that is not a size", Label("artifacts"),
		func(value, message string) {
			_, err := config.Load(write("repo: rosenhouse/lg\nartifact_max_bytes: " + value + "\n"))
			Expect(err).To(MatchError(HaveSuffix("config.yaml: line 2: " + message)))
			Expect(err).To(BeAssignableToTypeOf(config.Error("")))
		},
		Entry("negative", "-1", `want a size such as 500MB, not "-1"`),
		Entry("a fraction", "1.5GB", `want a size such as 500MB, not "1.5GB"`),
		Entry("an unknown unit", "5mb", `want a size such as 500MB, not "5mb"`),
		Entry("no number", "MB", `want a size such as 500MB, not "MB"`),
		Entry("too large", "9999999TiB", `size "9999999TiB" is too large`),
		Entry("a sequence", "[1MB]", "want a size such as 500MB, not !!seq"),
	)

	DescribeTable("rejects an artifact_max_bytes of 0", Label("artifacts"),
		func(zero string) {
			_, err := config.Load(write("repo: rosenhouse/lg\nartifact_max_bytes: " + zero + "\n"))
			Expect(err).To(MatchError(config.Error("artifact_max_bytes must be at least 1B")))
		},
		Entry("bare", "0"),
		Entry("with a unit", "0GB"),
	)

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
		Entry("dot segments", "../.."),
		Entry("dot owner", "./lg"),
		Entry("dot name", "rosenhouse/."),
		Entry("dot-dot name", "rosenhouse/.."),
	)

	DescribeTable("treats a file with no YAML document as having no repo",
		func(yaml string) {
			_, err := config.Load(write(yaml))
			Expect(err).To(MatchError(config.Error(`repo must be owner/name: ""`)))
		},
		Entry("empty", ""),
		Entry("only a comment", "# repo: rosenhouse/lg\n"),
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

	DescribeTable("rejects an api_url that is not an absolute http or https URL without user info, query or fragment, hiding any password",
		func(apiURL, shown string) {
			_, err := config.Load(write("repo: rosenhouse/lg\napi_url: '" + apiURL + "'\n"))
			Expect(err).To(MatchError(config.Error(`api_url must be an http or https URL with no user info, query or fragment: "` + shown + `"`)))
		},
		Entry("no scheme", "127.0.0.1:18301", "127.0.0.1:18301"),
		Entry("another scheme", "ftp://127.0.0.1", "ftp://127.0.0.1"),
		Entry("no host", "http://", "http://"),
		Entry("user info", "http://user:s3cr3t@127.0.0.1/api/v3", "http://user:xxxxx@127.0.0.1/api/v3"),
		Entry("a query", "http://127.0.0.1?x=1", "http://127.0.0.1?x=1"),
		Entry("an empty query", "http://127.0.0.1?", "http://127.0.0.1?"),
		Entry("a fragment", "http://127.0.0.1#frag", "http://127.0.0.1#frag"),
		Entry("an empty fragment", "http://127.0.0.1#", "http://127.0.0.1#"),
	)

	DescribeTable("accepts an api_url on host or api.<host> over https, or on a loopback address", Label("transport"),
		func(host, apiURL string) {
			_, err := config.Load(write("host: " + host + "\nrepo: rosenhouse/lg\napi_url: '" + apiURL + "'\n"))
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("api.<host>", "github.com", "https://api.github.com"),
		Entry("host itself", "ghe.corp.example", "https://ghe.corp.example/api/v3"),
		Entry("host in upper case", "ghe.corp.example", "https://GHE.CORP.EXAMPLE/api/v3"),
		Entry("127.0.0.1 over http", "github.com", "http://127.0.0.1:1"),
		Entry("::1 over http", "github.com", "http://[::1]:1"),
		Entry("localhost over http", "github.com", "http://localhost:1"),
	)

	DescribeTable("rejects an api_url that could send host's token elsewhere", Label("transport"),
		func(host, apiURL, reason string) {
			_, err := config.Load(write("host: " + host + "\nrepo: rosenhouse/lg\napi_url: '" + apiURL + "'\n"))
			Expect(err).To(MatchError(config.Error(reason + `: "` + apiURL + `"`)))
		},
		Entry("another host", "github.com", "https://ghe.corp.example/api/v3", "api_url must be on host, api.<host> or a loopback address"),
		Entry("a subdomain of api.<host>", "github.com", "https://x.api.github.com", "api_url must be on host, api.<host> or a loopback address"),
		Entry("a host that only starts with host", "github.com", "https://github.com.example", "api_url must be on host, api.<host> or a loopback address"),
		Entry("a non-loopback IPv4 address", "github.com", "http://192.0.2.1:1", "api_url must be on host, api.<host> or a loopback address"),
		Entry("a non-loopback IPv6 address over https", "github.com", "https://[2001:db8::1]", "api_url must be on host, api.<host> or a loopback address"),
		Entry("http to api.<host>", "github.com", "http://api.github.com", "api_url must use https unless it is on a loopback address"),
		Entry("http to host", "ghe.corp.example", "http://ghe.corp.example/api/v3", "api_url must use https unless it is on a loopback address"),
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
