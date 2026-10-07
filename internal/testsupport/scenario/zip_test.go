package scenario_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// zipFiles reads every file of a zip.
func zipFiles(data []byte) map[string][]byte {
	GinkgoHelper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	files := map[string][]byte{}
	for _, f := range r.File {
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		files[f.Name], err = io.ReadAll(rc)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.Close()).To(Succeed())
	}
	return files
}

// tarGzFiles reads every regular file of a tar.gz.
func tarGzFiles(data []byte) map[string][]byte {
	GinkgoHelper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	Expect(err).NotTo(HaveOccurred())
	r := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		Expect(err).NotTo(HaveOccurred())
		if h.Typeflag == tar.TypeReg {
			files[h.Name], err = io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
		}
	}
}

var _ = Describe("BuildArtifactZip", Label("extract"), func() {
	It("gives the same bytes each time: top.log, inner.zip holding zip/nested.log, and inner.tar.gz holding tgz/nested.log with the text", func() {
		built := scenario.BuildArtifactZip("foo bar")
		Expect(scenario.BuildArtifactZip("foo bar")).To(Equal(built))

		files := zipFiles(built)
		Expect(files).To(HaveLen(3))
		Expect(files).To(HaveKey("top.log"))
		Expect(zipFiles(files["inner.zip"])).To(HaveKey("zip/nested.log"))
		Expect(tarGzFiles(files["inner.tar.gz"])).To(Equal(map[string][]byte{"tgz/nested.log": []byte("foo bar\n")}))
	})
})

var _ = Describe("WithArtifactZip", Label("extract"), func() {
	It("serves the zip as the artifact's, listed with its size and sha256 digest, leaving the others and the original run unchanged", func() {
		run := scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), 7)
		zip := []byte("not really a zip")

		replaced := scenario.WithArtifactZip(run, 7_011276272069, zip)

		Expect(replaced.Files["artifacts/7011276272069.zip"].Data).To(Equal(zip))
		sum := sha256.Sum256(zip)
		Expect(artifacts(replaced)).To(ContainElement(SatisfyAll(
			HaveKeyWithValue("id", BeEquivalentTo(7_011276272069)),
			HaveKeyWithValue("size_in_bytes", BeEquivalentTo(len(zip))),
			HaveKeyWithValue("digest", "sha256:"+hex.EncodeToString(sum[:])),
		)))
		Expect(artifacts(replaced)).To(ContainElements(artifacts(run)[0], artifacts(run)[2], artifacts(run)[3]))
		Expect(run.Files["artifacts/7011276272069.zip"].Data).NotTo(Equal(zip))
	})
})
