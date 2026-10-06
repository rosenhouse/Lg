package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/status"
	"github.com/rosenhouse/lg/internal/store"
)

const requestWait = 10 * time.Second

// Request adds a sync request to state/sync-request under state/request.lock,
// and gives its number. The number follows status.json's served_request too,
// so a removed or garbled state/sync-request never makes a request look served.
func Request(fsys store.FS, state string, clk clock.Clock) (int64, error) {
	held, err := lock.Wait(filepath.Join(state, "request.lock"), requestWait, clk, func(string) {})
	if err != nil {
		return 0, err
	}
	defer func() { _ = held.Release() }()
	n, _ := Requested(state)
	if st, _ := status.Read(filepath.Join(state, "status.json")); st != nil && st.ServedRequest < math.MaxInt64 {
		n = max(n, st.ServedRequest)
	}
	n++
	return n, store.ReplaceFileFS(fsys, requestFile(state), fmt.Appendf(nil, "%d\n", n))
}

// Requested gives the number of the latest sync request, or 0 before the first.
func Requested(state string) (int64, error) {
	path := requestFile(state)
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(string(bytes.TrimSpace(content)), 10, 64)
	if err == nil && (n < 0 || n == math.MaxInt64) {
		err = fmt.Errorf("%d is no request number", n)
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}

func requestFile(state string) string { return filepath.Join(state, "sync-request") }
