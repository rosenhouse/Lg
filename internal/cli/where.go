package cli

type whereCmd struct {
	Hits []string `arg:"" optional:"" name:"path|hit"`
}

func (whereCmd) Run(*Deps) error { return nil }
