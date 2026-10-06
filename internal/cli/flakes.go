package cli

type flakesCmd struct {
	Kind    string `default:"all" enum:"rerun,all" help:"Report this kind of flake (${enum})."`
	filters `embed:""`
	JSON    bool `name:"json" help:"Print one JSON object per finding."`
}

func (f flakesCmd) Validate() error { return f.validate() }

func (f flakesCmd) Run(deps *Deps) error { return nil }
