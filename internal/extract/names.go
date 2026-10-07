package extract

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/rosenhouse/lg/internal/layout"
)

const (
	maxComponent = 200
	maxDepth     = 32
)

// namer gives each member a path below extracted/ that no other member has,
// even on a case-insensitive filesystem.
type namer struct{ root node }

// node is a dir, keyed by folded name.
type node map[string]*entry

type entry struct {
	name string
	kind kind
	node node
}

type kind int

const (
	file kind = iota
	dir
	// expansion is the dir that a nested archive expands into.
	expansion
)

func newNamer() *namer {
	return &namer{root: node{fold(Manifest): {name: Manifest, kind: file}}}
}

func fold(name string) string { return strings.ToLower(name) }

// file gives the path for a member named name of the archive expanding into
// base, and why it differs from name, if it does.
func (n *namer) file(base []string, name string) (string, string) {
	parts, reason := components(name)
	if keep := maxDepth - len(base) - 1; len(parts) > keep+1 {
		parts = append(parts[:keep:keep], layout.Slug(strings.Join(parts[keep:], "/")))
		reason = first(reason, "too_deep")
	}
	parent := n.at(base)
	placed := append([]string(nil), base...)
	for i, part := range parts {
		k := dir
		if i == len(parts)-1 {
			k = file
		}
		actual, child, renamed := parent.place(part, k)
		if renamed {
			reason = first(reason, "collision")
		}
		placed, parent = append(placed, actual), child
	}
	return strings.Join(placed, "/"), reason
}

// dir gives the components of the dir that the archive at rel expands
// into, and why its name is not rel.d, if it is not.
func (n *namer) dir(rel string) ([]string, string) {
	parts := strings.Split(rel, "/")
	parent := n.at(parts[:len(parts)-1])
	actual, _, renamed := parent.place(parts[len(parts)-1]+".d", expansion)
	reason := ""
	if renamed {
		reason = "collision"
	}
	return append(parts[:len(parts)-1:len(parts)-1], actual), reason
}

// at gives the node of a dir already placed.
func (n *namer) at(names []string) node {
	at := n.root
	for _, name := range names {
		at = at[fold(name)].node
	}
	return at
}

// place names a child of kind in the dir, reusing a dir of that name, else
// suffixing ~N to a name that is taken.
func (d node) place(name string, k kind) (string, node, bool) {
	if e, ok := d[fold(name)]; ok && k == dir && e.kind == dir && e.name == name {
		return name, e.node, false
	}
	actual := name
	for i := 1; d[fold(actual)] != nil; i++ {
		suffix := fmt.Sprintf("~%d", i)
		actual = truncate(name, maxComponent-len(suffix)) + suffix
	}
	e := &entry{name: actual, kind: k}
	if k != file {
		e.node = node{}
	}
	d[fold(actual)] = e
	return actual, e.node, actual != name
}

// components splits a member's name into names for files and dirs, slugifying
// one that is too long or that a filesystem may refuse.
func components(name string) ([]string, string) {
	clean := path.Clean(name)
	if clean == "." {
		return []string{"none"}, "invalid"
	}
	parts := strings.Split(clean, "/")
	reason := ""
	for i, part := range parts {
		switch {
		case len(part) > maxComponent:
			parts[i], reason = layout.Slug(part), first(reason, "too_long")
		case strings.ContainsRune(part, 0) || !utf8.ValidString(part):
			parts[i], reason = layout.Slug(part), first(reason, "invalid")
		}
	}
	return parts, reason
}

// truncate cuts s, which is valid UTF-8, to at most n bytes, at a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func first(reason, next string) string {
	if reason != "" {
		return reason
	}
	return next
}
