package fakeservice_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFakeservice(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fakeservice Suite")
}
