package config_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("Load", Label("discovery"), func() {
	write := func(yaml string) string {
		path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(path, []byte(yaml), 0o600)).To(Succeed())
		return path
	}

	DescribeTable("reads durations such as 10m, 36h and 7d",
		func(value string, d time.Duration) {
			Expect(config.Load(write("repo: rosenhouse/lg\nsync_interval: " + value + "\n"))).To(HaveField("SyncInterval", config.Duration(d)))
		},
		Entry("minutes", "10m", 10*time.Minute),
		Entry("hours", "36h", 36*time.Hour),
		Entry("days", "7d", 7*scenario.Day),
	)

	It("accepts the most days a duration holds and rejects one more", func() {
		Expect(config.Load(write("repo: rosenhouse/lg\nretention: 106751d\n"))).To(HaveField("Retention", config.Duration(106751*scenario.Day)))
		_, err := config.Load(write("repo: rosenhouse/lg\nretention: 106752d\n"))
		Expect(err).To(MatchError(HaveSuffix(`config.yaml: line 2: want a duration such as 7d or 36h, not "106752d"`)))
	})

	DescribeTable("rejects a duration in days that is not a whole number",
		func(value string) {
			_, err := config.Load(write("repo: rosenhouse/lg\nretention: " + value + "\n"))
			Expect(err).To(MatchError(HaveSuffix(`config.yaml: line 2: want a duration such as 7d or 36h, not "` + value + `"`)))
		},
		Entry("a fraction", "1.5d"),
		Entry("negative", "-1d"),
	)

	It("rejects a sync_interval under 1m, naming the key", func() {
		_, err := config.Load(write("repo: rosenhouse/lg\nsync_interval: 59s\n"))
		Expect(err).To(invalid("sync_interval must be at least 1m: 59s"))
	})

	DescribeTable("rejects a backfill or retention that is not positive, naming the key",
		func(line, message string) {
			_, err := config.Load(write("repo: rosenhouse/lg\n" + line + "\n"))
			Expect(err).To(invalid(message))
		},
		Entry("a negative backfill", "backfill: -24h", "backfill must be positive: -1d"),
		Entry("a zero backfill", "backfill: 0", "backfill must be positive: 0s"),
		Entry("a zero retention", "retention: 0d", "retention must be positive: 0s"),
		Entry("a negative retention", "retention: -1h", "retention must be positive: -1h"),
	)

	It("names the file before a value it cannot use", func() {
		path := write("")
		_, err := config.Load(path)
		Expect(err).To(MatchError(config.Error(path + `: repo must be owner/name: ""`)))
	})

	It("rejects a second YAML document, naming the file", func() {
		path := write("repo: rosenhouse/lg\n---\nrepo: other/repo\n")
		_, err := config.Load(path)
		Expect(err).To(MatchError(config.Error(path + ": more than one YAML document")))
	})

	It("rejects a backfill longer than retention, naming both", func() {
		_, err := config.Load(write("repo: rosenhouse/lg\nbackfill: 10d\nretention: 36h\n"))
		Expect(err).To(invalid("backfill must not exceed retention: 10d > 36h"))
	})
})

var _ = Describe("Defaults", Label("discovery"), func() {
	It("gives sync_interval 10m, backfill 7d, retention 90d, disk_cap 50GB, artifact_max_bytes 500MB and log_grace 1h", func() {
		Expect(config.Defaults()).To(Equal(config.Config{
			Host:             "github.com",
			SyncInterval:     config.Duration(10 * time.Minute),
			Backfill:         config.Duration(7 * scenario.Day),
			Retention:        config.Duration(90 * scenario.Day),
			DiskCap:          50_000_000_000,
			ArtifactMaxBytes: 500_000_000,
			LogGrace:         config.Duration(time.Hour),
		}))
	})
})

var _ = Describe("Duration", Label("discovery"), func() {
	DescribeTable("prints whole days as days",
		func(d time.Duration, s string) {
			Expect(config.Duration(d).String()).To(Equal(s))
		},
		Entry("days", 90*scenario.Day, "90d"),
		Entry("hours", 36*time.Hour, "36h"),
		Entry("minutes", 10*time.Minute, "10m"),
		Entry("seconds", 59*time.Second, "59s"),
		Entry("mixed", 90*time.Minute, "1h30m"),
		Entry("zero", time.Duration(0), "0s"),
	)
})
