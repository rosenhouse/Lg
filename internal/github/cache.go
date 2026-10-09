package github

import (
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
)

// Cache holds answers to GETs by URL, so that GitHub can confirm one with a
// 304, which costs no rate limit. It is not safe for concurrent use.
type Cache struct {
	answers map[string]Answer
	asked   map[string]bool
}

// Answer is a 200's ETag, Link and body.
type Answer struct {
	ETag string `json:"etag"`
	Link string `json:"link,omitempty"`
	Body []byte `json:"body"`
}

func NewCache(earlier map[string]Answer) *Cache {
	answers := maps.Clone(earlier)
	if answers == nil {
		answers = map[string]Answer{}
	}
	return &Cache{answers: answers, asked: map[string]bool{}}
}

// Asked gives the answers it holds for the URLs asked for since NewCache.
func (c *Cache) Asked() map[string]Answer {
	asked := map[string]Answer{}
	for rawURL := range c.asked {
		if a, ok := c.answers[rawURL]; ok {
			asked[rawURL] = a
		}
	}
	return asked
}

// All gives every answer it holds.
func (c *Cache) All() map[string]Answer { return maps.Clone(c.answers) }

// WithCache makes h revalidate its GETs of the repo and of run listings
// without a created range, whose URLs repeat. h is then not safe for
// concurrent use.
func (h *HTTP) WithCache(c *Cache) *HTTP {
	h.cache = c
	return h
}

// revalidate GETs rawURL as get does. When the cache holds an answer for
// rawURL, it sends that answer's ETag, and reads that answer on a 304. It
// caches a 200 with an ETag that read accepts, and forgets an answer that
// read rejects.
func (h *HTTP) revalidate(ctx context.Context, rawURL string, read func(*http.Response) error) error {
	if h.cache == nil {
		return h.get(ctx, rawURL, read)
	}
	h.cache.asked[rawURL] = true
	earlier := h.cache.answers[rawURL]
	return h.getIfNoneMatch(ctx, rawURL, earlier.ETag, func(resp *http.Response) error {
		answer := earlier
		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			answer = Answer{ETag: resp.Header.Get("ETag"), Link: resp.Header.Get("Link"), Body: body}
		}
		// A copy, since getIfNoneMatch closes resp.Body.
		answered := *resp
		answered.Header, answered.Body = http.Header{"Link": {answer.Link}}, io.NopCloser(bytes.NewReader(answer.Body))
		if err := read(&answered); err != nil {
			delete(h.cache.answers, rawURL)
			return err
		}
		if answer.ETag != "" {
			h.cache.answers[rawURL] = answer
		}
		return nil
	})
}
