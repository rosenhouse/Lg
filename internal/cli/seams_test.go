package cli_test

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
)

var _ = Describe("lg sync", Label("failures"), func() {
	It("builds its GitHub client through Deps.NewGitHub", func() {
		fake := fakegithub.Start(37129390741, "after-attempt-1")
		fake.Fail("blob", "/logs/111221289888.txt", fakegithub.Fault{Truncate: true, Stall: true})
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\napi_url: "+fake.URL()+"\n"), 0o644)).To(Succeed())
		short := github.Timeouts{Dial: time.Second, TLSHandshake: time.Second, ResponseHeader: time.Second, BodyIdle: 200 * time.Millisecond}

		var stderr bytes.Buffer
		code := make(chan int, 1)
		go func() {
			code <- cli.Main([]string{"sync"}, cli.Deps{
				Env:    map[string]string{"LG_HOME": GinkgoT().TempDir(), "LG_CONFIG": config, "LG_GH": "gh", "LG_TEST_NOW": "2026-10-03T18:00:00Z"},
				Stdout: &bytes.Buffer{},
				Stderr: &stderr,
				Clock:  clock.Real{},
				Runner: tokenRunner{},
				NewGitHub: func(api *url.URL, repo, token string, clk clock.Clock) github.Client {
					return github.NewHTTP(github.NewTransport(short), api, repo, token, clk)
				},
				StoreFS: store.OSFS{},
			})
		}()

		Eventually(code, 10*time.Second).Should(Receive(Equal(1)))
		Expect(stderr.String()).To(ContainSubstring("idle for 200ms"))
	})
})

var _ = Describe("RealDeps", Label("failures"), func() {
	It("builds GitHub clients with github.NewDefault", func() {
		Expect(reflect.ValueOf(cli.RealDeps().NewGitHub).Pointer()).To(Equal(reflect.ValueOf(github.NewDefault).Pointer()))
	})

	It("opens the store on the OS filesystem", Label("blocked"), func() {
		Expect(cli.RealDeps().StoreFS).To(Equal(store.OSFS{}))
	})
})

type tokenRunner struct{}

func (tokenRunner) Run(context.Context, string, []string, map[string]string) (stdout, stderr []byte, err error) {
	return []byte("lg-test-token\n"), nil, nil
}
