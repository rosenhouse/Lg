package scenario

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"testing/fstest"
	"time"
)

// zipTime is the modification time of every file BuildArtifactZip writes.
var zipTime = time.Date(2026, 10, 3, 14, 23, 0, 0, time.UTC)

// BuildArtifactZip gives a zip holding top.log, inner.zip holding
// zip/nested.log, and inner.tar.gz holding tgz/nested.log, a line of text.
func BuildArtifactZip(text string) []byte {
	return zipOf(map[string][]byte{
		"top.log":      []byte("LG_MARKER artifact top-level\n"),
		"inner.zip":    zipOf(map[string][]byte{"zip/nested.log": []byte("LG_MARKER artifact nested in zip\n")}),
		"inner.tar.gz": tarGzOf("tgz/nested.log", []byte(text+"\n")),
	})
}

func zipOf(files map[string][]byte) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime})
		must(err)
		_, err = f.Write(files[name])
		must(err)
	}
	must(w.Close())
	return buf.Bytes()
}

func tarGzOf(name string, data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := tar.NewWriter(gz)
	must(w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data)), ModTime: zipTime}))
	_, err := w.Write(data)
	must(err)
	must(w.Close())
	must(gz.Close())
	return buf.Bytes()
}

// WithArtifactZip serves zip as the artifact's zip, listing its size and digest.
func WithArtifactZip(r Run, artifactID int64, zip []byte) Run {
	sum := sha256.Sum256(zip)
	out := r.editArtifact(artifactID, func(artifact map[string]any) {
		artifact["size_in_bytes"] = len(zip)
		artifact["digest"] = "sha256:" + hex.EncodeToString(sum[:])
	})
	out.Files[fmt.Sprintf("artifacts/%d.zip", artifactID)] = &fstest.MapFile{Data: zip}
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
