package cli_test

import (
	"bytes"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
)

var _ = Describe("lg paths", Label("sync"), func() {
	var (
		home           string
		stdout, stderr *bytes.Buffer
	)

	BeforeEach(func() {
		home = filepath.Join(GinkgoT().TempDir(), "lg")
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	})

	paths := func() int {
		return cli.Main([]string{"paths"}, cli.Deps{Env: map[string]string{"LG_HOME": home}, Stdout: stdout, Stderr: stderr})
	}

	writeFormat := func() {
		Expect(os.MkdirAll(home, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(home, "FORMAT"), []byte("lg-store 1\n"), 0o644)).To(Succeed())
	}

	mkfile := func(elem ...string) string {
		path := filepath.Join(append([]string{home}, elem...)...)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, []byte("log"), 0o644)).To(Succeed())
		return path
	}

	It("prints only regular log.txt files under data/", func() {
		writeFormat()
		log := mkfile("data", "a", "log.txt")
		Expect(os.MkdirAll(filepath.Join(home, "data", "b"), 0o755)).To(Succeed())
		Expect(os.Symlink(log, filepath.Join(home, "data", "b", "log.txt"))).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(home, "data", "c", "log.txt"), 0o755)).To(Succeed())
		mkfile("tmp", "unit-1", "jobs", "1_x", "log.txt")

		Expect(paths()).To(Equal(0))
		Expect(stdout.String()).To(Equal(log + "\n"))
	})

	It("prints nothing and exits 0 before the first sync creates data/", func() {
		Expect(paths()).To(Equal(0))
		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(BeEmpty())
	})

	It("exits 1 naming data/ when data/ is a dangling symlink", func() {
		Expect(os.MkdirAll(home, 0o755)).To(Succeed())
		data := filepath.Join(home, "data")
		Expect(os.Symlink(filepath.Join(home, "unmounted"), data)).To(Succeed())

		Expect(paths()).To(Equal(1))
		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(ContainSubstring(data))
	})

	It("prints paths under data/ when data/ is a symlink", func() {
		elsewhere := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(elsewhere, "a"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(elsewhere, "a", "log.txt"), []byte("log"), 0o644)).To(Succeed())
		writeFormat()
		Expect(os.Symlink(elsewhere, filepath.Join(home, "data"))).To(Succeed())

		Expect(paths()).To(Equal(0))
		Expect(stdout.String()).To(Equal(filepath.Join(home, "data", "a", "log.txt") + "\n"))
	})
})
