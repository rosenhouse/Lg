package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Host   string `yaml:"host"`
	Repo   string `yaml:"repo"`
	APIURL string `yaml:"api_url"`
}

var (
	ownerName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	hostName  = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
)

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, Error(err.Error())
	}
	cfg := Config{Host: "github.com"}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, Error(fmt.Sprintf("%s: %s", path, err))
	}
	cfg.Host = strings.ToLower(cfg.Host)
	if !hostName.MatchString(cfg.Host) {
		return Config{}, Error(fmt.Sprintf("host must be a host name: %q", cfg.Host))
	}
	if !ownerName.MatchString(cfg.Repo) || slices.ContainsFunc(strings.Split(cfg.Repo, "/"), isDots) {
		return Config{}, Error(fmt.Sprintf("repo must be owner/name: %q", cfg.Repo))
	}
	if cfg.APIURL != "" {
		if shown, ok := baseURL(cfg.APIURL); !ok {
			return Config{}, Error(fmt.Sprintf("api_url must be an http or https URL with no user info, query or fragment: %q", shown))
		}
	}
	return cfg, nil
}

func isDots(s string) bool { return s == "." || s == ".." }

// baseURL reports whether lg can append API paths to s, and shows s with any password hidden.
func baseURL(s string) (shown string, ok bool) {
	u, err := url.Parse(s)
	if err != nil {
		return s, false
	}
	if u.User != nil {
		return u.Redacted(), false
	}
	return s, (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && !strings.ContainsAny(s, "?#")
}
