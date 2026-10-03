// Package treesnap snapshots a directory tree to check that it only grows.
package treesnap

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

// Snap maps each path under the root, relative to it, to its Entry.
type Snap map[string]Entry

// Entry leaves ModTime zero for a dir, whose mtime changes when a child is added.
type Entry struct {
	Mode    fs.FileMode
	Size    int64
	ModTime time.Time
	SHA256  string
}

// Snapshot walks root without following symlinks. A missing root is empty.
func Snapshot(root string) Snap {
	ginkgo.GinkgoHelper()
	snap := Snap{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snap[rel], err = entry(path, d)
		return err
	})
	if errors.Is(err, fs.ErrNotExist) && len(snap) == 0 {
		return snap
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return snap
}

func entry(path string, d fs.DirEntry) (Entry, error) {
	info, err := d.Info()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{Mode: info.Mode()}
	if d.IsDir() {
		return e, nil
	}
	e.Size, e.ModTime = info.Size(), info.ModTime()
	if d.Type().IsRegular() {
		content, err := os.ReadFile(path)
		if err != nil {
			return Entry{}, err
		}
		e.SHA256 = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	return e, nil
}

// BeAppendOnlyFrom succeeds when the actual Snap still holds every entry of before, unchanged.
func BeAppendOnlyFrom(before Snap) types.GomegaMatcher {
	return &appendOnly{before: before}
}

type appendOnly struct {
	before  Snap
	changed []string
}

func (m *appendOnly) Match(actual any) (bool, error) {
	after, ok := actual.(Snap)
	if !ok {
		return false, fmt.Errorf("BeAppendOnlyFrom expects a treesnap.Snap, got %T", actual)
	}
	m.changed = nil
	for path, was := range m.before {
		if now, ok := after[path]; !ok {
			m.changed = append(m.changed, path+": removed")
		} else if now != was {
			m.changed = append(m.changed, fmt.Sprintf("%s: %+v became %+v", path, was, now))
		}
	}
	sort.Strings(m.changed)
	return len(m.changed) == 0, nil
}

func (m *appendOnly) FailureMessage(any) string {
	return "Expected the tree to keep every earlier entry unchanged, but:\n" + strings.Join(m.changed, "\n")
}

func (m *appendOnly) NegatedFailureMessage(any) string {
	return "Expected an earlier entry to change or disappear, but none did"
}
