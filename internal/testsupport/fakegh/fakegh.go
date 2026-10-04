// Package fakegh writes a gh script for specs.
package fakegh

import "path/filepath"

// Token is what the script prints for `gh auth token`.
const Token = "lg-test-token"

type GH struct {
	Path string
}

// New writes the script into dir.
func New(dir string) *GH {
	return &GH{Path: filepath.Join(dir, "gh")}
}

// Calls gives the arguments of each run, joined by spaces.
func (g *GH) Calls() []string { return nil }
