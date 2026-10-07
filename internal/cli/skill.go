package cli

import (
	"fmt"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/store"
	skill "github.com/rosenhouse/lg/skill/lg"
)

type skillCmd struct {
	Install skillInstallCmd `cmd:"" help:"Install lg's skill for Claude Code."`
}

type skillInstallCmd struct{}

func (skillInstallCmd) Run(deps *Deps) error {
	path, err := config.SkillFile(deps.Env)
	if err != nil {
		return err
	}
	if err := store.ReplaceFileUnlocked(deps.StoreFS, path, []byte(skill.Markdown), deps.Clock.Now()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Stdout, path)
	return err
}
