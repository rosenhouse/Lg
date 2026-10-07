package doctest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDoctest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Doctest Suite")
}
