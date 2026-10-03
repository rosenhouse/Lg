package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/config"
)

var _ = Describe("Locations", Label("cli"), func() {
	DescribeTable("resolves the store as LG_HOME > XDG_DATA_HOME/lg > HOME/.local/share/lg",
		func(env map[string]string, home string) {
			roots, err := config.Locations(env)
			Expect(err).NotTo(HaveOccurred())
			Expect(roots.Home).To(Equal(home))
		},
		Entry("LG_HOME wins", map[string]string{"LG_HOME": "/lg", "XDG_DATA_HOME": "/xdg", "HOME": "/h"}, "/lg"),
		Entry("XDG_DATA_HOME next", map[string]string{"XDG_DATA_HOME": "/xdg", "HOME": "/h"}, "/xdg/lg"),
		Entry("HOME last", map[string]string{"HOME": "/h"}, "/h/.local/share/lg"),
		Entry("empty values count as unset", map[string]string{"LG_HOME": "", "XDG_DATA_HOME": "", "HOME": "/h"}, "/h/.local/share/lg"),
	)
})
