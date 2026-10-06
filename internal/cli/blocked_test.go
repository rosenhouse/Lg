package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

var _ = DescribeTable("lg sync blocks as local_io when opening the store fails", Label("blocked"),
	func(fault func(home string, fsys *faultfs.FS), detail string) {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		fsys := faultfs.New()
		fault(home, fsys)
		var stderr bytes.Buffer

		code := cli.Main([]string{"sync"}, cli.Deps{
			Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
			Stdout:  &bytes.Buffer{},
			Stderr:  &stderr,
			Clock:   clock.Real{},
			Runner:  &countingRunner{},
			StoreFS: fsys,
		})

		Expect(code).To(Equal(3))
		Expect(stderr.String()).To(HavePrefix("lg: warning: never synced\nlg: blocked (local_io): "))
		Expect(stderr.String()).To(ContainSubstring(detail))
	},
	Entry("EROFS making data/",
		func(_ string, fsys *faultfs.FS) { fsys.FailOn("mkdir", syscall.EROFS) },
		"read-only file system"),
	Entry("EACCES sweeping tmp/",
		func(home string, fsys *faultfs.FS) {
			Expect(store.Init(home)).To(Succeed())
			Expect(os.Mkdir(filepath.Join(home, "tmp", "unit-left"), 0o755)).To(Succeed())
			fsys.FailOn("remove", syscall.EACCES)
		},
		"permission denied"),
	Entry("tmp/ on another mount than data/",
		func(home string, fsys *faultfs.FS) {
			real, err := store.OSFS{}.Mount(home)
			Expect(err).NotTo(HaveOccurred())
			fsys.SetMount(filepath.Join(home, "tmp"), store.Mount{Dev: real.Dev + 1, ID: real.ID + 1})
		},
		"different devices or mounts"),
)
