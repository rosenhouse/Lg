package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/config"
)

var _ = Describe("Locations", Label("cli"), func() {
	DescribeTable("resolves the store as LG_HOME > XDG_DATA_HOME/lg > HOME/.local/share/lg",
		func(env map[string]string, store string) {
			roots, err := config.Locations(env)
			Expect(err).NotTo(HaveOccurred())
			Expect(roots.Store).To(Equal(store))
		},
		Entry("LG_HOME wins", map[string]string{"LG_HOME": "/lg", "XDG_DATA_HOME": "/xdg", "HOME": "/h"}, "/lg"),
		Entry("XDG_DATA_HOME next", map[string]string{"XDG_DATA_HOME": "/xdg", "HOME": "/h"}, "/xdg/lg"),
		Entry("HOME last", map[string]string{"HOME": "/h"}, "/h/.local/share/lg"),
		Entry("empty values count as unset", map[string]string{"LG_HOME": "", "XDG_DATA_HOME": "", "HOME": "/h"}, "/h/.local/share/lg"),
		Entry("relative XDG_DATA_HOME counts as unset", map[string]string{"XDG_DATA_HOME": "rel", "HOME": "/h"}, "/h/.local/share/lg"),
		Entry("LG_HOME needs no HOME", map[string]string{"LG_HOME": "/lg"}, "/lg"),
		Entry("LG_HOME ignores a relative HOME", map[string]string{"LG_HOME": "/lg", "HOME": "rel"}, "/lg"),
	)

	It("puts data/, state/ and tmp/ under the store", func() {
		roots, err := config.Locations(map[string]string{"LG_HOME": "/lg"})
		Expect(err).NotTo(HaveOccurred())
		Expect(roots.Data).To(Equal("/lg/data"))
		Expect(roots.State).To(Equal("/lg/state"))
		Expect(roots.Tmp).To(Equal("/lg/tmp"))
	})

	DescribeTable("returns a config.Error",
		func(env map[string]string, msg string) {
			_, err := config.Locations(env)
			Expect(err).To(MatchError(config.Error(msg)))
		},
		Entry("when nothing locates the store", map[string]string{"XDG_DATA_HOME": "rel"},
			"cannot locate the store: set LG_HOME, an absolute XDG_DATA_HOME or HOME"),
		Entry("for a relative LG_HOME", map[string]string{"LG_HOME": "lgdata", "HOME": "/h"},
			`LG_HOME must be an absolute path: "lgdata"`),
		Entry("for an LG_HOME starting with ~", map[string]string{"LG_HOME": "~/lg", "HOME": "/h"},
			`LG_HOME must be an absolute path: "~/lg"`),
		Entry("for a relative HOME", map[string]string{"HOME": "relhome"},
			`HOME must be an absolute path: "relhome"`),
		Entry("for a store at the filesystem root", map[string]string{"LG_HOME": "//."},
			`LG_HOME must not be the filesystem root: "//."`),
	)
})

var _ = Describe("File", Label("cli"), func() {
	DescribeTable("resolves the config file as LG_CONFIG > XDG_CONFIG_HOME/lg/config.yaml > HOME/.config/lg/config.yaml",
		func(env map[string]string, configFile string) {
			Expect(config.File(env)).To(Equal(configFile))
		},
		Entry("LG_CONFIG wins", map[string]string{"LG_CONFIG": "/c.yaml", "XDG_CONFIG_HOME": "/xdg", "HOME": "/h"}, "/c.yaml"),
		Entry("XDG_CONFIG_HOME next", map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/h"}, "/xdg/lg/config.yaml"),
		Entry("HOME last", map[string]string{"HOME": "/h"}, "/h/.config/lg/config.yaml"),
		Entry("empty values count as unset", map[string]string{"LG_CONFIG": "", "XDG_CONFIG_HOME": "", "HOME": "/h"}, "/h/.config/lg/config.yaml"),
		Entry("relative XDG_CONFIG_HOME counts as unset", map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/h"}, "/h/.config/lg/config.yaml"),
		Entry("LG_HOME does not move it", map[string]string{"LG_HOME": "/lg", "HOME": "/h"}, "/h/.config/lg/config.yaml"),
	)

	DescribeTable("returns a config.Error",
		func(env map[string]string, msg string) {
			_, err := config.File(env)
			Expect(err).To(MatchError(config.Error(msg)))
		},
		Entry("when nothing locates the config file", map[string]string{"LG_HOME": "/lg"},
			"cannot locate config.yaml: set LG_CONFIG, an absolute XDG_CONFIG_HOME or HOME"),
		Entry("for a relative LG_CONFIG", map[string]string{"LG_CONFIG": "c.yaml", "HOME": "/h"},
			`LG_CONFIG must be an absolute path: "c.yaml"`),
		Entry("for a relative HOME", map[string]string{"HOME": "relhome"},
			`HOME must be an absolute path: "relhome"`),
	)
})

var _ = Describe("SkillFile", Label("skill"), func() {
	DescribeTable("resolves lg's skill as CLAUDE_CONFIG_DIR/skills/lg/SKILL.md > HOME/.claude/skills/lg/SKILL.md",
		func(env map[string]string, skillFile string) {
			Expect(config.SkillFile(env)).To(Equal(skillFile))
		},
		Entry("CLAUDE_CONFIG_DIR wins", map[string]string{"CLAUDE_CONFIG_DIR": "/c", "HOME": "/h"}, "/c/skills/lg/SKILL.md"),
		Entry("HOME last", map[string]string{"HOME": "/h"}, "/h/.claude/skills/lg/SKILL.md"),
		Entry("empty values count as unset", map[string]string{"CLAUDE_CONFIG_DIR": "", "HOME": "/h"}, "/h/.claude/skills/lg/SKILL.md"),
	)

	DescribeTable("returns a config.Error",
		func(env map[string]string, msg string) {
			_, err := config.SkillFile(env)
			Expect(err).To(MatchError(config.Error(msg)))
		},
		Entry("when nothing locates it", map[string]string{},
			"cannot locate SKILL.md: set CLAUDE_CONFIG_DIR or HOME"),
		Entry("for a relative CLAUDE_CONFIG_DIR", map[string]string{"CLAUDE_CONFIG_DIR": "c", "HOME": "/h"},
			`CLAUDE_CONFIG_DIR must be an absolute path: "c"`),
	)
})
