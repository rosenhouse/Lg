package cli

type pathsCmd struct {
	Null bool `short:"0" help:"Separate paths with NUL instead of newline."`
}

func (pathsCmd) Run(*Deps) error { return nil }
