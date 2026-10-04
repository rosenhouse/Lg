package treesnap_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTreesnap(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Treesnap Suite")
}
