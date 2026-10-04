package failure_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFailure(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Failure Suite")
}
