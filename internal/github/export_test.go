package github

import (
	"net/http"
	"time"
)

func TransportOf(c *http.Client) (*http.Transport, time.Duration) {
	t := c.Transport.(*idleTransport)
	return t.base, t.bodyIdle
}
