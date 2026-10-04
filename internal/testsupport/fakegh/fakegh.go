// Package fakegh writes a gh script for specs.
package fakegh

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// Token is what the script prints for `gh auth token`.
const Token = "lg-test-token"

type GH struct {
	Path string
	dir  string
}

// New writes the script into dir. It prints the token in dir/token and
// appends its arguments to dir/calls and its GH_CONFIG_DIR to dir/config-dirs.
func New(dir string) *GH {
	ginkgo.GinkgoHelper()
	g := &GH{Path: filepath.Join(dir, "gh"), dir: dir}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> '%s'\nprintf '%%s\\n' \"$GH_CONFIG_DIR\" >> '%s'\ncat '%s'\n",
		g.file("calls"), g.file("config-dirs"), g.file("token"))
	g.SetToken(Token)
	gomega.Expect(os.WriteFile(g.Path, []byte(script), 0o755)).To(gomega.Succeed())
	return g
}

// SetToken makes the script print token.
func (g *GH) SetToken(token string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.WriteFile(g.file("token"), []byte(token+"\n"), 0o644)).To(gomega.Succeed())
}

// Calls gives the arguments of each run, joined by spaces.
func (g *GH) Calls() []string {
	ginkgo.GinkgoHelper()
	return g.lines("calls")
}

// ConfigDirs gives the GH_CONFIG_DIR of each run.
func (g *GH) ConfigDirs() []string {
	ginkgo.GinkgoHelper()
	return g.lines("config-dirs")
}

func (g *GH) lines(name string) []string {
	ginkgo.GinkgoHelper()
	data, err := os.ReadFile(g.file(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func (g *GH) file(name string) string { return filepath.Join(g.dir, name) }
