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

	DescribeTable("resolves the config file as LG_CONFIG > XDG_CONFIG_HOME/lg/config.yaml > HOME/.config/lg/config.yaml",
		func(env map[string]string, configFile string) {
			roots, err := config.Locations(env)
			Expect(err).NotTo(HaveOccurred())
			Expect(roots.ConfigFile).To(Equal(configFile))
		},
		Entry("LG_CONFIG wins", map[string]string{"LG_CONFIG": "/c.yaml", "XDG_CONFIG_HOME": "/xdg", "HOME": "/h"}, "/c.yaml"),
		Entry("XDG_CONFIG_HOME next", map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/h"}, "/xdg/lg/config.yaml"),
		Entry("HOME last", map[string]string{"HOME": "/h"}, "/h/.config/lg/config.yaml"),
		Entry("empty values count as unset", map[string]string{"LG_CONFIG": "", "XDG_CONFIG_HOME": "", "HOME": "/h"}, "/h/.config/lg/config.yaml"),
		Entry("LG_HOME does not move it", map[string]string{"LG_HOME": "/lg", "HOME": "/h"}, "/h/.config/lg/config.yaml"),
	)

	It("puts state/ and tmp/ beside data/", func() {
		roots, err := config.Locations(map[string]string{"LG_HOME": "/lg"})
		Expect(err).NotTo(HaveOccurred())
		Expect(roots.Data).To(Equal("/lg/data"))
		Expect(roots.State).To(Equal("/lg/state"))
		Expect(roots.Tmp).To(Equal("/lg/tmp"))
	})

	DescribeTable("fails when a root falls back to an unset HOME",
		func(env map[string]string) {
			_, err := config.Locations(env)
			Expect(err).To(MatchError(ContainSubstring("HOME is not set")))
		},
		Entry("for the store", map[string]string{"LG_CONFIG": "/c.yaml"}),
		Entry("for the config file", map[string]string{"LG_HOME": "/lg"}),
	)

	It("needs no HOME when both roots are set", func() {
		_, err := config.Locations(map[string]string{"LG_HOME": "/lg", "XDG_CONFIG_HOME": "/xdg"})
		Expect(err).NotTo(HaveOccurred())
	})
})
