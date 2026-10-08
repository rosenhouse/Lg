package grep_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGrep(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Grep Suite")
}
