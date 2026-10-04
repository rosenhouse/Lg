package config_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/config"
)

const day = 24 * time.Hour

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
		Entry("days", "7d", 7*day),
	)

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
		Expect(err).To(MatchError(config.Error("sync_interval must be at least 1m: 59s")))
	})

	It("rejects a backfill longer than retention, naming both", func() {
		_, err := config.Load(write("repo: rosenhouse/lg\nbackfill: 10d\nretention: 36h\n"))
		Expect(err).To(MatchError(config.Error("backfill must not exceed retention: 10d > 36h")))
	})
})

var _ = Describe("Defaults", Label("discovery"), func() {
	It("gives sync_interval 10m, backfill 7d, retention 90d, disk_cap 50GB, artifact_max_bytes 500MB and log_grace 1h", func() {
		Expect(config.Defaults()).To(Equal(config.Config{
			Host:             "github.com",
			SyncInterval:     config.Duration(10 * time.Minute),
			Backfill:         config.Duration(7 * day),
			Retention:        config.Duration(90 * day),
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
		Entry("days", 90*day, "90d"),
		Entry("hours", 36*time.Hour, "36h"),
		Entry("minutes", 10*time.Minute, "10m"),
		Entry("seconds", 59*time.Second, "59s"),
		Entry("mixed", 90*time.Minute, "1h30m"),
		Entry("zero", time.Duration(0), "0s"),
	)
})
