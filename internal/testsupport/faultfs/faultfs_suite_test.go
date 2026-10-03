package faultfs_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFaultfs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Faultfs Suite")
}
