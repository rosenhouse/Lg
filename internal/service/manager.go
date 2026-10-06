package service

import (
	"context"

	"github.com/rosenhouse/lg/internal/execx"
)

type Manager struct {
	GOOS   string
	Runner execx.Runner
	Env    map[string]string
	UID    int
}

func (Manager) Install(context.Context, Unit) (string, error) { return "", nil }

func (Manager) Uninstall(context.Context, string) (string, error) { return "", nil }
