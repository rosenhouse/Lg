package cli

type indexCmd struct {
	Rebuild indexRebuildCmd `cmd:"" help:"Rebuild state/lg.db from data/."`
}

type indexRebuildCmd struct{}

func (indexRebuildCmd) Run(*Deps) error { return nil }
