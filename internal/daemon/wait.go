package daemon

import (
	"errors"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
)

func WaitForCycle(state string, request int64, timeout time.Duration, clk clock.Clock) error {
	return errors.New("not yet")
}
