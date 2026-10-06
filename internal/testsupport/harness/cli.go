package harness

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/execx"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

// CLI runs cli.Main in-process, with a store on FS, against Fake serving the
// fixture run, with LG_TEST_NOW at DefaultNow.
type CLI struct {
	Home, Config   string
	Fake           *fakegithub.Server
	FS             *faultfs.FS
	Runner         execx.Runner
	Stdout, Stderr bytes.Buffer
}

func NewCLI() *CLI {
	ginkgo.GinkgoHelper()
	c := &CLI{
		Home:   ginkgo.GinkgoT().TempDir(),
		Config: filepath.Join(ginkgo.GinkgoT().TempDir(), "config.yaml"),
		Fake:   fakegithub.Start(37129390741, "after-attempt-1"),
		FS:     faultfs.New(),
		Runner: TokenRunner{},
	}
	c.WriteConfig("repo: rosenhouse/lg\napi_url: " + c.Fake.URL() + "\n")
	return c
}

func (c *CLI) WriteConfig(content string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.WriteFile(c.Config, []byte(content), 0o644)).To(gomega.Succeed())
}

// Main runs lg with args, and asserts that a sync left every earlier file
// under data/ unchanged.
func (c *CLI) Main(args ...string) int {
	ginkgo.GinkgoHelper()
	c.Stdout.Reset()
	c.Stderr.Reset()
	data := filepath.Join(c.Home, "data")
	before := treesnap.Snapshot(data)
	code := cli.Main(args, cli.Deps{
		Env:    map[string]string{"LG_HOME": c.Home, "LG_CONFIG": c.Config, "LG_GH": "gh", "LG_TEST_NOW": DefaultNow().Format(time.RFC3339)},
		Stdout: &c.Stdout,
		Stderr: &c.Stderr,
		Clock:  clock.Real{},
		Runner: c.Runner,
		NewGitHub: func(api *url.URL, repo, token string, clk clock.Clock) github.Client {
			return github.NewHTTP(github.NewTransport(ShortTimeouts()), api, repo, token, clk)
		},
		StoreFS: c.FS,
	})
	if len(args) > 0 && args[0] == "sync" {
		gomega.Expect(treesnap.Snapshot(data)).To(treesnap.BeAppendOnlyFrom(before))
	}
	return code
}

func (c *CLI) StatusFile() string { return filepath.Join(c.Home, "state", "status.json") }

// WriteStatus initializes the store and replaces its state/status.json with raw.
func (c *CLI) WriteStatus(raw string) {
	ginkgo.GinkgoHelper()
	writeStatus(c.Home, c.StatusFile(), raw)
}

// Status decodes state/status.json.
func (c *CLI) Status() map[string]any {
	ginkgo.GinkgoHelper()
	return ReadStatus(c.StatusFile())
}

// TokenRunner is a gh that prints fakegh.Token.
type TokenRunner struct{}

func (TokenRunner) Run(context.Context, string, []string, map[string]string) (stdout, stderr []byte, err error) {
	return []byte(fakegh.Token + "\n"), nil, nil
}
