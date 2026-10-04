package cli

type initCmd struct {
	Repo string `required:"" help:"Repository as owner/name."`
	Host string `default:"github.com" help:"GitHub host."`
}

func (c initCmd) Run(deps *Deps) error {
	return nil
}
