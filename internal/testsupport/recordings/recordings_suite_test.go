package recordings_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRecordings(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Recordings Suite")
}
