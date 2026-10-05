package cli_test

import (
	"bytes"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/store"
)

var _ = Describe("lg index rebuild", Label("index"), func() {
	It("exits 2 naming LG_HOME, and makes nothing, when LG_HOME does not exist", func() {
		home := filepath.Join(GinkgoT().TempDir(), "typo")
		var stderr bytes.Buffer

		code := cli.Main([]string{"index", "rebuild"}, cli.Deps{Env: map[string]string{"LG_HOME": home}, Stdout: &bytes.Buffer{}, Stderr: &stderr, Clock: clock.Real{}})
		Expect(code).To(Equal(2))
		Expect(stderr.String()).To(ContainSubstring("LG_HOME"))
		Expect(stderr.String()).To(ContainSubstring(home))
		Expect(home).NotTo(BeAnExistingFile())
	})

	It("exits 2 and leaves an empty LG_HOME that lg can still make a store", func() {
		home := GinkgoT().TempDir()

		code := cli.Main([]string{"index", "rebuild"}, cli.Deps{Env: map[string]string{"LG_HOME": home}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Clock: clock.Real{}})
		Expect(code).To(Equal(2))
		Expect(os.ReadDir(home)).To(BeEmpty())
		Expect(store.Init(home)).To(Succeed())
	})

	It("makes state/ again when it is missing", func() {
		home := GinkgoT().TempDir()
		Expect(store.Init(home)).To(Succeed())
		Expect(os.RemoveAll(filepath.Join(home, "state"))).To(Succeed())
		var stderr bytes.Buffer

		code := cli.Main([]string{"index", "rebuild"}, cli.Deps{Env: map[string]string{"LG_HOME": home}, Stdout: &bytes.Buffer{}, Stderr: &stderr, Clock: clock.Real{}})
		Expect(code).To(Equal(0), stderr.String())
		Expect(filepath.Join(home, "state", "lg.db")).To(BeARegularFile())
	})
})
