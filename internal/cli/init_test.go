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
			"sync_interval (10m), backfill (7d), retention (90d), disk_cap (50GB), artifact_max_bytes (500MB) or log_grace (1h)."))
	})

	It("says what log_grace and api_url do", func() {
		c := harness.NewCLI()

		Expect(c.Main("init", "--help")).To(Equal(0))
		Expect(strings.Join(strings.Fields(c.Stdout.String()), " ")).To(ContainSubstring(
			"log_grace is how long lg retries a log or zip that GitHub answers with 404 before it writes a tombstone. " +
				"api_url replaces the API URL that lg derives from host."))
	})
})
