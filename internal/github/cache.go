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

// answer gives the answer held for rawURL, if any. A nil Cache holds none.
func (c *Cache) answer(rawURL string) Answer {
	if c == nil {
		return Answer{}
	}
	c.asked[rawURL] = true
	return c.answers[rawURL]
}

// keep holds a for rawURL, or nothing when a has no ETag.
func (c *Cache) keep(rawURL string, a Answer) {
	switch {
	case c == nil:
	case a.ETag == "":
		delete(c.answers, rawURL)
	default:
		c.answers[rawURL] = a
	}
}

func (a Answer) response() *http.Response {
	header := http.Header{}
	if a.Link != "" {
		header.Set("Link", a.Link)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(a.Body))}
}

// WithCache makes h revalidate its GETs of the repo, runs, run listings by
// status and artifact listings, whose URLs a later cycle repeats.
func (h *HTTP) WithCache(c *Cache) *HTTP {
	h.cache = c
	return h
}

// revalidate GETs rawURL as get does. When the cache holds an answer for
// rawURL, it sends that answer's ETag, and reads that answer on a 304. It
// caches a 200 that read accepts.
func (h *HTTP) revalidate(ctx context.Context, rawURL string, read func(*http.Response) error) error {
	earlier := h.cache.answer(rawURL)
	return h.getIfNoneMatch(ctx, rawURL, earlier.ETag, func(resp *http.Response) error {
		if resp.StatusCode == http.StatusNotModified {
			return read(earlier.response())
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		answer := Answer{ETag: resp.Header.Get("ETag"), Link: resp.Header.Get("Link"), Body: body}
		if err := read(answer.response()); err != nil {
			return err
		}
		h.cache.keep(rawURL, answer)
		return nil
	})
}
