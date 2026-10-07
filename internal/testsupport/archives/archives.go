// Package archives builds zip, tar and tar.gz archives for specs, with the
// same bytes each time.
package archives

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"time"
)

// Entry is a file of an archive. Mode holds type and permission bits; zero
// means a regular file with mode 0644. TarType, when set, overrides Mode's type.
type Entry struct {
	Name    string
	Body    string
	Mode    fs.FileMode
	TarType byte
	// Link is the target of a symlink or hardlink.
	Link string
}

// ModTime is the modification time of every entry.
var ModTime = time.Date(2026, 10, 3, 14, 23, 0, 0, time.UTC)

func (e Entry) mode() fs.FileMode {
	if e.Mode == 0 {
		return 0o644
	}
	return e.Mode
}

// Zip gives a zip of the entries, deflated. A symlink's body is its Link.
func Zip(entries ...Entry) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.Name, Method: zip.Deflate, Modified: ModTime}
		header.SetMode(e.mode())
		f, err := w.CreateHeader(header)
		must(err)
		body := e.Body
		if e.Mode&fs.ModeSymlink != 0 {
			body = e.Link
		}
		_, err = f.Write([]byte(body))
		must(err)
	}
	must(w.Close())
	return buf.Bytes()
}

// Tar gives a tar of the entries.
func Tar(entries ...Entry) []byte {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, e := range entries {
		header, err := tar.FileInfoHeader(info{e}, e.Link)
		must(err)
		header.Name, header.Linkname, header.ModTime, header.Uname, header.Gname = e.Name, e.Link, ModTime, "", ""
		if e.TarType != 0 {
			header.Typeflag = e.TarType
		}
		if header.Typeflag != tar.TypeReg {
			header.Size = 0
		}
		must(w.WriteHeader(header))
		_, err = w.Write([]byte(e.Body[:header.Size]))
		must(err)
	}
	must(w.Close())
	return buf.Bytes()
}

// TarGz gives a gzipped tar of the entries.
func TarGz(entries ...Entry) []byte { return Gzip(Tar(entries...)) }

// Gzip compresses data.
func Gzip(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.ModTime = ModTime
	_, err := w.Write(data)
	must(err)
	must(w.Close())
	return buf.Bytes()
}

// info describes an Entry to tar.FileInfoHeader.
type info struct{ e Entry }

func (i info) Name() string       { return i.e.Name }
func (i info) Size() int64        { return int64(len(i.e.Body)) }
func (i info) Mode() fs.FileMode  { return i.e.mode() }
func (i info) ModTime() time.Time { return ModTime }
func (i info) IsDir() bool        { return i.e.Mode.IsDir() }
func (i info) Sys() any           { return nil }

func must(err error) {
	if err != nil {
		panic(err)
	}
}
