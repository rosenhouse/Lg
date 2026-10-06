package daemon

import (
	"github.com/rosenhouse/lg/internal/clock"
)

type InstanceLock struct{}

func LockInstance(state string, clk clock.Clock) (*InstanceLock, error) { return &InstanceLock{}, nil }

func (l *InstanceLock) Release() error { return nil }

func Request(state string, clk clock.Clock) (int64, error) { return 0, nil }

func Requested(state string) (int64, error) { return 0, nil }
