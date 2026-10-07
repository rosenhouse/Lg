package cli_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg init --help", Label("discovery"), func() {
	It("names each config key with its default", func() {
		c := harness.NewCLI()

		Expect(c.Main("init", "--help")).To(Equal(0))
		Expect(strings.Join(strings.Fields(c.Stdout.String()), " ")).To(ContainSubstring(
			"sync_interval (10m), backfill (7d), retention (90d), disk_cap (50GB), artifact_max_bytes (500MB), log_grace (1h) or api_url."))
	})
})
