package cli_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/testsupport/doctest"
	skill "github.com/rosenhouse/lg/skill/lg"
)

var _ = Describe("SKILL.md", Label("skill"), func() {
	It("uses only commands and flags that the lg Kong parser accepts in every `lg …` line", func() {
		commands := doctest.LgCommands(skill.Markdown)
		Expect(commands).NotTo(BeEmpty())
		for _, c := range commands {
			Expect(cli.Parse(c.Args)).To(Succeed(), "line %d: lg %s", c.Line, strings.Join(c.Args, " "))
		}
	})
})
