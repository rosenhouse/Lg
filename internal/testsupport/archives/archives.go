// Package archives builds zip, tar and tar.gz archives for specs, with the
// same bytes each time.
package archives

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"cmp"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"hash/crc32"
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
	// Method is a zip member's compression method; zero means Deflate. A
	// method other than Store or Deflate keeps Body as it is.
	Method uint16
	// BadCRC gives a zip member a CRC-32 that its body does not match.
	BadCRC bool
}

// modTime is the modification time of every entry.
var modTime = time.Date(2026, 10, 3, 14, 23, 0, 0, time.UTC)

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
		header := &zip.FileHeader{Name: e.Name, Method: cmp.Or(e.Method, zip.Deflate), Modified: modTime}
		header.SetMode(e.mode())
		body := []byte(e.Body)
		if e.Mode&fs.ModeSymlink != 0 {
			body = []byte(e.Link)
		}
		if header.Method == zip.Deflate && !e.BadCRC {
			f, err := w.CreateHeader(header)
			must(err)
			_, err = f.Write(body)
			must(err)
			continue
		}
		writeRaw(w, header, body, e.BadCRC)
	}
	must(w.Close())
	return buf.Bytes()
}

// writeRaw writes body as the member of header, compressed by its method if
// that is Deflate, else as it is.
func writeRaw(w *zip.Writer, header *zip.FileHeader, body []byte, badCRC bool) {
	header.CRC32 = crc32.ChecksumIEEE(body)
	if badCRC {
		header.CRC32++
	}
	header.UncompressedSize64 = uint64(len(body))
	stored := body
	if header.Method == zip.Deflate {
		var buf bytes.Buffer
		fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
		must(err)
		_, err = fw.Write(body)
		must(err)
		must(fw.Close())
		stored = buf.Bytes()
	}
	header.CompressedSize64 = uint64(len(stored))
	f, err := w.CreateRaw(header)
	must(err)
	_, err = f.Write(stored)
	must(err)
}

// Tar gives a tar of the entries. A GNU sparse entry holds Body as one
// region of data.
func Tar(entries ...Entry) []byte {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, e := range entries {
		header, err := tar.FileInfoHeader(info{e}, e.Link)
		must(err)
		header.Name, header.Linkname, header.ModTime, header.Uname, header.Gname = e.Name, e.Link, modTime, "", ""
		if e.TarType != 0 {
			header.Typeflag = e.TarType
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeCont:
		case tar.TypeGNUSparse:
			header.Format = tar.FormatGNU
		default:
			header.Size = 0
		}
		must(w.WriteHeader(header))
		if header.Typeflag == tar.TypeGNUSparse {
			mapSparse(buf.Bytes()[buf.Len()-blockSize:], header.Size)
		}
		_, err = w.Write([]byte(e.Body[:header.Size]))
		must(err)
	}
	must(w.Close())
	return buf.Bytes()
}

const blockSize = 512

// mapSparse gives a GNU sparse header block one region of size bytes at
// offset 0, which Go's tar.Writer leaves empty.
func mapSparse(block []byte, size int64) {
	octal := func(offset int, v int64) { copy(block[offset:offset+12], fmt.Sprintf("%011o\x00", v)) }
	octal(386, 0)
	octal(398, size)
	octal(483, size)
	copy(block[148:156], "        ")
	var sum int64
	for _, b := range block {
		sum += int64(b)
	}
	copy(block[148:156], fmt.Sprintf("%06o\x00 ", sum))
}

// TarGz gives a gzipped tar of the entries.
func TarGz(entries ...Entry) []byte { return Gzip(Tar(entries...)) }

// Gzip compresses data.
func Gzip(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.ModTime = modTime
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
func (i info) ModTime() time.Time { return modTime }
func (i info) IsDir() bool        { return i.e.Mode.IsDir() }
func (i info) Sys() any           { return nil }

func must(err error) {
	if err != nil {
		panic(err)
	}
}
