package tombstone_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTombstone(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tombstone Suite")
}
