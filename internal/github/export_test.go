package github

import "net/http"

func TransportOf(rt http.RoundTripper) *http.Transport { return rt.(*idleTransport).base }

func TimeoutsOf(c Client) Timeouts { return c.(*HTTP).transport.(*idleTransport).timeouts }
