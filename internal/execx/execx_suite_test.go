package execx_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExecx(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Execx Suite")
}
