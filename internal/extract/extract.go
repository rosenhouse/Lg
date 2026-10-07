// Package extract expands an artifact's zip, and the archives nested in it,
// into the artifact's extracted/ dir.
package extract

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/version"
)

// Limits bound what Extract writes for one artifact.
type Limits struct {
	// MaxBytes bounds the bytes of every file written.
	MaxBytes int64
	// MaxNesting is how many levels of archives within artifact.zip are expanded.
	MaxNesting int
}

func Defaults() Limits { return Limits{MaxBytes: 1_000_000_000, MaxNesting: 8} }

const (
	// Manifest is the file in extracted/ that records how it was made.
	Manifest = ".lg-extract.json"
	source   = "artifact.zip"
)

// Extract expands artifactDir/artifact.zip into artifactDir/extracted,
// staged in tmp/ and published whole. Past limits.MaxBytes, it publishes
// nothing and returns store.ErrTooLarge.
func Extract(s *store.Store, artifactDir string, limits Limits, now time.Time) error {
	zipFile, err := os.Open(filepath.Join(artifactDir, source))
	if err != nil {
		return err
	}
	defer func() { _ = zipFile.Close() }()
	unit, err := s.NewUnit()
	if err != nil {
		return err
	}
	x := &extraction{unit: unit, limits: limits, names: newNamer(), manifest: newManifest(limits, now)}
	err = x.expandZip(zipFile, source, nil, 0)
	if errors.As(err, new(corrupt)) {
		err = fmt.Errorf("%s: %w", source, err)
	}
	if err == nil {
		x.manifest.Bytes = x.written
		err = writeJSON(unit, Manifest, x.manifest)
	}
	if err == nil {
		err = s.Publish(unit, filepath.Join(artifactDir, "extracted"))
	}
	if err != nil {
		return errors.Join(err, unit.Abort())
	}
	return nil
}

type extraction struct {
	unit     *store.Unit
	limits   Limits
	names    *namer
	manifest manifest
	written  int64
}

// member is a file of an archive.
type member struct {
	name string
	// skip is why the member is not written, or "".
	skip   string
	setuid bool
	open   func() (io.ReadCloser, error)
}

// corrupt is an archive that does not read.
type corrupt struct{ error }

func (x *extraction) expandZip(f *os.File, archive string, dir []string, level int) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	r, err := zip.NewReader(f, info.Size())
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return corrupt{err}
	}
	for _, file := range r.File {
		mode := file.Mode()
		if mode.IsDir() {
			continue
		}
		m := member{name: file.Name, skip: skipped(mode), setuid: mode&(fs.ModeSetuid|fs.ModeSetgid) != 0, open: file.Open}
		if err := x.write(m, archive, dir, level); err != nil {
			return err
		}
	}
	return nil
}

func (x *extraction) expandTar(r io.Reader, archive string, dir []string, level int) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil && !errors.Is(err, tar.ErrInsecurePath) {
			return corrupt{err}
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		m := member{name: h.Name, skip: tarSkipped(h.Typeflag), setuid: h.Mode&0o6000 != 0, open: func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }}
		if err := x.write(m, archive, dir, level); err != nil {
			return err
		}
	}
}

// skipped gives why a zip member of mode is not written, or "".
func skipped(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "symlink"
	case mode&fs.ModeDevice != 0:
		return "device"
	case mode&fs.ModeNamedPipe != 0:
		return "fifo"
	case !mode.IsRegular():
		return "special"
	}
	return ""
}

// tarSkipped gives why a tar member of typeflag is not written, or "".
func tarSkipped(typeflag byte) string {
	switch typeflag {
	case tar.TypeReg:
		return ""
	case tar.TypeSymlink:
		return "symlink"
	case tar.TypeLink:
		return "hardlink"
	case tar.TypeChar, tar.TypeBlock:
		return "device"
	case tar.TypeFifo:
		return "fifo"
	}
	return "special"
}

// write writes m below dir, the components of the dir its archive expands
// into, and expands it when it is an archive.
func (x *extraction) write(m member, archive string, dir []string, level int) error {
	rec := record{Archive: archive, Name: m.name}
	if m.skip == "" {
		m.skip = escapes(m.name)
	}
	if m.skip != "" {
		rec.Reason = m.skip
		x.manifest.Skipped = append(x.manifest.Skipped, rec)
		return nil
	}
	rel, reason := x.names.file(dir, m.name)
	rec.Path = rel
	if reason != "" {
		rec.Reason = reason
		x.manifest.Renamed = append(x.manifest.Renamed, rec)
	}
	if m.setuid {
		x.manifest.SetuidDropped = append(x.manifest.SetuidDropped, record{Archive: archive, Name: m.name, Path: rel})
	}
	if err := x.copy(m, rel); err != nil {
		return err
	}
	return x.expandNested(rel, archive, m.name, level+1)
}

// escapes gives why an archive member's name points outside extracted/, or "".
func escapes(name string) string {
	if strings.HasPrefix(name, "/") {
		return "absolute"
	}
	if clean := path.Clean(name); clean == ".." || strings.HasPrefix(clean, "../") {
		return "outside"
	}
	return ""
}

func (x *extraction) copy(m member, rel string) error {
	r, err := m.open()
	if err != nil {
		return corrupt{err}
	}
	defer func() { _ = r.Close() }()
	w, err := x.unit.Create(rel, x.limits.MaxBytes-x.written)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, readErrors{r})
	if err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	sum, err := x.unit.Sum(rel)
	x.written += sum.Bytes
	return err
}

// readErrors marks the errors of reading an archive as corrupt.
type readErrors struct{ r io.Reader }

func (r readErrors) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = corrupt{err}
	}
	return n, err
}

var (
	zipMagic      = []byte("PK\x03\x04")
	emptyZipMagic = []byte("PK\x05\x06")
	gzipMagic     = []byte("\x1f\x8b")
)

// expandNested expands the file at rel, a member of archive, into rel.d/
// when its bytes begin as a zip, a tar, or a gzipped tar.
func (x *extraction) expandNested(rel, archive, name string, level int) error {
	f, err := x.unit.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	kind, err := sniff(f)
	if kind == "" || err != nil {
		return err
	}
	if level > x.limits.MaxNesting {
		x.manifest.NotExpanded = append(x.manifest.NotExpanded, record{Archive: archive, Name: name, Path: rel, Reason: "nesting"})
		return nil
	}
	dir, reason := x.names.dir(rel)
	if reason != "" {
		x.manifest.Renamed = append(x.manifest.Renamed, record{Archive: archive, Name: name + ".d", Path: strings.Join(dir, "/"), Reason: reason})
	}
	switch kind {
	case "zip":
		err = x.expandZip(f, rel, dir, level)
	case "tar.gz":
		var gz *gzip.Reader
		if gz, err = gzip.NewReader(f); err == nil {
			err = x.expandTar(gz, rel, dir, level)
		}
	case "tar":
		err = x.expandTar(f, rel, dir, level)
	}
	var bad corrupt
	if errors.As(err, &bad) {
		x.manifest.NotExpanded = append(x.manifest.NotExpanded, record{Archive: archive, Name: name, Path: rel, Reason: kind + ": " + bad.Error()})
		return nil
	}
	return err
}

// sniff names the kind of archive f holds, by its first bytes, and rewinds f.
func sniff(f *os.File) (string, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	head = head[:n]
	kind := ""
	switch {
	case bytes.HasPrefix(head, zipMagic) || bytes.HasPrefix(head, emptyZipMagic):
		kind = "zip"
	case isTar(head):
		kind = "tar"
	case bytes.HasPrefix(head, gzipMagic):
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		if gz, err := gzip.NewReader(bufio.NewReader(f)); err == nil {
			inner := make([]byte, 512)
			n, _ := io.ReadFull(gz, inner)
			if isTar(inner[:n]) {
				kind = "tar.gz"
			}
		}
	}
	_, err = f.Seek(0, io.SeekStart)
	return kind, err
}

// isTar reports whether block begins with a ustar header.
func isTar(block []byte) bool {
	return len(block) >= 262 && string(block[257:262]) == "ustar"
}

func (c corrupt) Error() string { return c.error.Error() }

// manifest is extracted/.lg-extract.json.
type manifest struct {
	LgFormat    int       `json:"lg_format"`
	LgVersion   string    `json:"lg_version"`
	ExtractedAt time.Time `json:"extracted_at"`
	MaxBytes    int64     `json:"max_bytes"`
	Bytes       int64     `json:"bytes"`
	Renamed     []record  `json:"renamed"`
	Skipped     []record  `json:"skipped"`
	// SetuidDropped lists the files written without their setuid and setgid bits.
	SetuidDropped []record `json:"setuid_dropped"`
	NotExpanded   []record `json:"not_expanded"`
}

func newManifest(limits Limits, now time.Time) manifest {
	return manifest{
		LgFormat: 1, LgVersion: version.Version, ExtractedAt: now.UTC().Truncate(time.Second), MaxBytes: limits.MaxBytes,
		Renamed: []record{}, Skipped: []record{}, SetuidDropped: []record{}, NotExpanded: []record{},
	}
}

// record is a member of an archive, which is artifact.zip or a path below extracted/.
type record struct {
	Archive string `json:"archive"`
	Name    string `json:"name"`
	// Path is where the member went below extracted/.
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func writeJSON(unit *store.Unit, name string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return unit.WriteJSON(name, buf.Bytes())
}
