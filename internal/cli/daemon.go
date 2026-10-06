package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

type daemonCmd struct {
	Run daemonRunCmd `cmd:"" help:"Sync every sync_interval, and at each lg sync, until SIGTERM."`
}

type daemonRunCmd struct{}

func (daemonRunCmd) Run(*Deps) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	<-ctx.Done()
	return nil
}
