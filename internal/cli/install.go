package cli

type daemonInstallCmd struct {
	Name string `hidden:"" default:"lg" help:"Name the unit and label after this instead."`
}

func (daemonInstallCmd) Run(*Deps) error { return nil }

type daemonUninstallCmd struct {
	Name string `hidden:"" default:"lg" help:"Name of the unit and label to remove."`
}

func (daemonUninstallCmd) Run(*Deps) error { return nil }
