package extract

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/rosenhouse/lg/internal/layout"
)

const (
	maxComponent = 200
	maxDepth     = 32
	// maxPath bounds a path below extracted/, before ~N suffixes, so it fits
	// macOS's PATH_MAX of 1024 below a typical store.
	maxPath = 512
)

// namer gives each member a path below extracted/ that no other member has,
// even on a filesystem that ignores case and Unicode normalization.
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
	return &namer{root: node{fold(layout.ExtractManifest): {name: layout.ExtractManifest, kind: file}}}
}

// fold maps names that a case- and normalization-insensitive filesystem,
// such as APFS, takes as one to one key.
func fold(name string) string { return strings.ToLower(norm.NFC.String(name)) }

// file gives the path for a member named name of the archive expanding into
// base, and why it differs from name, if it does.
func (n *namer) file(base []string, name string) (string, string) {
	parts, reason := components(name)
	if keep := maxDepth - len(base) - 1; len(parts) > keep+1 {
		parts = collapse(parts, keep)
		reason = first(reason, "too_deep")
	}
	if pathLen(base, parts) > maxPath {
		parts, reason = shorten(base, parts), first(reason, "too_long")
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

// collapse joins parts from keep on into one slug.
func collapse(parts []string, keep int) []string {
	return append(parts[:keep:keep], layout.Slug(strings.Join(parts[keep:], "/")))
}

// shorten collapses as few of parts below base as fit their path in maxPath.
func shorten(base, parts []string) []string {
	for keep := len(parts) - 1; keep > 0; keep-- {
		shorter := collapse(parts, keep)
		if pathLen(base, shorter) <= maxPath {
			return shorter
		}
	}
	return collapse(parts, 0)
}

func pathLen(base, parts []string) int {
	return len(strings.Join(append(base[:len(base):len(base)], parts...), "/"))
}

// dir gives the components of the dir that the archive at rel expands
// into, and why its name is not rel.d, if it is not.
func (n *namer) dir(rel string) ([]string, string) {
	parts := strings.Split(rel, "/")
	parent := n.at(parts[:len(parts)-1])
	name := parts[len(parts)-1]
	reason := ""
	if len(name)+len(".d") > maxComponent {
		name, reason = truncate(name, maxComponent-len(".d")), "too_long"
	}
	actual, _, renamed := parent.place(name+".d", expansion)
	if renamed {
		reason = first(reason, "collision")
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
// one that is too long, that a filesystem may refuse, or whose newline lg
// paths would refuse.
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
		case strings.ContainsFunc(part, isControl) || !utf8.ValidString(part):
			parts[i], reason = layout.Slug(part), first(reason, "invalid")
		}
	}
	return parts, reason
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

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
