package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing/fstest"

	"github.com/rosenhouse/lg/internal/testsupport/archives"
)

// BuildArtifactZip gives a zip holding top.log, inner.zip holding
// zip/nested.log, and inner.tar.gz holding tgz/nested.log, a line of text.
func BuildArtifactZip(text string) []byte {
	return archives.Zip(
		archives.Entry{Name: "inner.tar.gz", Body: string(archives.TarGz(archives.Entry{Name: "tgz/nested.log", Body: text + "\n"}))},
		archives.Entry{Name: "inner.zip", Body: string(archives.Zip(archives.Entry{Name: "zip/nested.log", Body: "LG_MARKER artifact nested in zip\n"}))},
		archives.Entry{Name: "top.log", Body: "LG_MARKER artifact top-level\n"},
	)
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
