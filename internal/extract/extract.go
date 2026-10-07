// Package extract expands an artifact's zip, and the archives nested in it,
// into the artifact's extracted/ dir.
package extract

import (
	"archive/tar"
	"context"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/version"
)

// Limits bound what Extract writes for one artifact.
type Limits struct {
	// MaxBytes bounds the total bytes of the files written.
	MaxBytes int64
	// MaxFiles bounds the members of all archives together, skipped ones included.
	// Past it, artifact.zip fails whole, and a nested archive stays unexpanded.
	MaxFiles int
	// MaxNesting is how many levels of archives within artifact.zip are expanded.
	MaxNesting int
}

// Defaults gives the limits lg extract uses.
func Defaults() Limits { return Limits{MaxBytes: 1_000_000_000, MaxFiles: 100_000, MaxNesting: 8} }

// ErrTooManyFiles is an artifact.zip of more than Limits.MaxFiles members.
var ErrTooManyFiles = errors.New("too many files")

// errFull is a member of a nested archive past Limits.MaxFiles.
var errFull = errors.New("too_many_files")

const source = "artifact.zip"

// Extract expands artifactDir/artifact.zip into artifactDir/extracted,
// staged in tmp/ and published whole. Past limits.MaxBytes, it publishes
// nothing and returns store.ErrTooLarge, and when artifact.zip alone has
// more than limits.MaxFiles members, ErrTooManyFiles.
func Extract(ctx context.Context, s *store.Store, artifactDir string, limits Limits, now time.Time) error {
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
		err = unit.WriteValue(layout.ExtractManifest, x.manifest)
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
	// files counts the files written.
	files int
	// members counts the members met.
	members int
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
	var members []member
	var declared uint64
	for _, zf := range r.File {
		mode := zf.Mode()
		m := member{name: zf.Name, skip: skipped(mode), setuid: mode&(fs.ModeSetuid|fs.ModeSetgid) != 0, open: zf.Open}
		if mode.IsDir() {
			if zf.UncompressedSize64 == 0 {
				continue
			}
			m.skip = "dir_with_data"
		}
		if m.skip == "" && escapes(m.name) == "" {
			declared = min(declared+min(zf.UncompressedSize64, math.MaxInt64), math.MaxInt64)
		}
		members = append(members, m)
	}
	if level == 0 {
		if len(members) > x.limits.MaxFiles {
			return fmt.Errorf("%w: more than %d", ErrTooManyFiles, x.limits.MaxFiles)
		}
		// Nested archives only add bytes, so what artifact.zip declares is
		// a floor on what Extract would write.
		if declared > uint64(x.limits.MaxBytes) {
			return store.ErrTooLarge
		}
		x.members = len(members)
	}
	for _, m := range members {
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

// tarSkipped gives why a tar member of typeflag is not written, or "". Go's
// tar reader serves a sparse or contiguous file's data as a regular file's.
func tarSkipped(typeflag byte) string {
	switch typeflag {
	case tar.TypeReg, tar.TypeGNUSparse, tar.TypeCont:
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
// into, and expands it when it is an archive. A member of artifact.zip, at
// level 0, was counted already.
func (x *extraction) write(m member, archive string, dir []string, level int) error {
	if level > 0 {
		if x.members >= x.limits.MaxFiles {
			return errFull
		}
		x.members++
	}
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
	var bad corrupt
	if err := x.copy(m, rel); errors.As(err, &bad) {
		rec.Reason = "corrupt: " + bad.Error()
		x.manifest.Skipped = append(x.manifest.Skipped, rec)
		return nil
	} else if err != nil {
		return err
	}
	x.files++
	rec.Path = rel
	if reason != "" {
		rec.Reason = reason
		x.manifest.Renamed = append(x.manifest.Renamed, rec)
	}
	if m.setuid {
		x.manifest.SetuidDropped = append(x.manifest.SetuidDropped, record{Archive: archive, Name: m.name, Path: rel})
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
	// A member writes none of a chunk past its cap, so n is what it holds.
	n, err := io.Copy(w, readErrors{r})
	x.written += n
	if closeErr := w.Close(); err == nil {
		return closeErr
	}
	if errors.As(err, new(corrupt)) {
		if removeErr := x.unit.Remove(rel); removeErr != nil {
			return removeErr
		}
	}
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
	zipMagic  = []byte("PK\x03\x04")
	gzipMagic = []byte("\x1f\x8b")
)

// expandNested expands the file at rel, a member of archive, into rel.d/
// when its bytes begin as a zip, a tar, or a gzipped tar.
func (x *extraction) expandNested(rel, archive, name string, level int) error {
	f, err := x.unit.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	format, err := sniff(f)
	if format == "" || err != nil {
		return err
	}
	notExpanded := func(reason string) error {
		x.manifest.NotExpanded = append(x.manifest.NotExpanded, record{Archive: archive, Name: name, Path: rel, Reason: reason})
		return nil
	}
	switch {
	case level > x.limits.MaxNesting:
		return notExpanded("nesting")
	case strings.Count(rel, "/")+1 >= maxDepth:
		return notExpanded("too_deep")
	case len(rel)+len(".d/")+minName > maxPath:
		return notExpanded("too_long")
	case x.members >= x.limits.MaxFiles:
		return notExpanded(errFull.Error())
	}
	dir, reason := x.names.dir(rel)
	if reason != "" {
		x.manifest.Renamed = append(x.manifest.Renamed, record{Archive: archive, Name: name + ".d", Path: strings.Join(dir, "/"), Reason: reason})
	}
	files := x.files
	switch format {
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
	var why string
	switch {
	case errors.Is(err, errFull):
		why = err.Error()
	case errors.As(err, &bad):
		why = bad.Error()
		if !strings.HasPrefix(why, format+": ") {
			why = format + ": " + why
		}
	default:
		return err
	}
	if x.files > files {
		why = "partial: " + why
	}
	return notExpanded(why)
}

// sniff names the format of the archive f holds, by its first bytes, and rewinds f.
func sniff(f *os.File) (string, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	head = head[:n]
	format := ""
	switch {
	case bytes.HasPrefix(head, zipMagic):
		format = "zip"
	case isTar(head):
		format = "tar"
	case bytes.HasPrefix(head, gzipMagic):
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		if gz, err := gzip.NewReader(bufio.NewReader(f)); err == nil {
			inner := make([]byte, 512)
			n, _ := io.ReadFull(gz, inner)
			if isTar(inner[:n]) {
				format = "tar.gz"
			}
		}
	}
	_, err = f.Seek(0, io.SeekStart)
	return format, err
}

// isTar reports whether block begins with a ustar header.
func isTar(block []byte) bool {
	return len(block) >= 262 && string(block[257:262]) == "ustar"
}

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
