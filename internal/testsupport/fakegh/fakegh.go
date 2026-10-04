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
// appends its arguments to dir/calls.
func New(dir string) *GH {
	ginkgo.GinkgoHelper()
	g := &GH{Path: filepath.Join(dir, "gh"), dir: dir}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> '%s'\ncat '%s'\n", g.file("calls"), g.file("token"))
	gomega.Expect(os.WriteFile(g.file("token"), []byte(Token+"\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(g.Path, []byte(script), 0o755)).To(gomega.Succeed())
	return g
}

// Calls gives the arguments of each run, joined by spaces.
func (g *GH) Calls() []string {
	ginkgo.GinkgoHelper()
	calls, err := os.ReadFile(g.file("calls"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return strings.Split(strings.TrimSuffix(string(calls), "\n"), "\n")
}

func (g *GH) file(name string) string { return filepath.Join(g.dir, name) }
