package github

import (
	"net"
	"net/http"
	"time"
)

func TransportOf(c *http.Client) (*http.Transport, *net.Dialer, time.Duration) {
	t := c.Transport.(*idleTransport)
	return t.base, t.dialer, t.bodyIdle
}
