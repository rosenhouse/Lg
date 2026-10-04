package github

import (
	"net/http"
	"time"
)

func TransportOf(rt http.RoundTripper) (*http.Transport, time.Duration) {
	t := rt.(*idleTransport)
	return t.base, t.bodyIdle
}
