package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Host   string `yaml:"host"`
	Repo   string `yaml:"repo"`
	APIURL string `yaml:"api_url"`

	LogGrace Duration `yaml:"log_grace"`
}

// Duration is a Go duration such as 1h or 0s, or a bare 0.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: want a duration such as 1h, not %s", node.Line, node.ShortTag())
	}
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*d = Duration(parsed)
	return nil
}

var (
	ownerName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	hostName  = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
)

func Defaults() Config {
	return Config{Host: "github.com", LogGrace: Duration(time.Hour)}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, Error(err.Error())
	}
	cfg := Defaults()
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
	if cfg.LogGrace < 0 {
		return Config{}, Error(fmt.Sprintf("log_grace must not be negative: %q", time.Duration(cfg.LogGrace)))
	}
	if cfg.APIURL != "" {
		if err := checkAPIURL(cfg.APIURL, cfg.Host); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// checkAPIURL accepts only an api_url that can be given host's token.
func checkAPIURL(apiURL, host string) error {
	u, shown, ok := baseURL(apiURL)
	if !ok {
		return Error(fmt.Sprintf("api_url must be an http or https URL with no user info, query or fragment: %q", shown))
	}
	if onLoopback(u) {
		return nil
	}
	name := strings.ToLower(u.Hostname())
	if name != host && name != "api."+host {
		return Error(fmt.Sprintf("api_url must be on host, api.<host> or a loopback address: %q", apiURL))
	}
	if u.Scheme != "https" {
		return Error(fmt.Sprintf("api_url must use https unless it is on a loopback address: %q", apiURL))
	}
	return nil
}

// IsLoopback reports whether apiURL is on a loopback address, where only a test fakegithub should listen.
func IsLoopback(apiURL string) bool {
	u, err := url.Parse(apiURL)
	return err == nil && onLoopback(u)
}

func onLoopback(u *url.URL) bool {
	name := strings.ToLower(u.Hostname())
	ip := net.ParseIP(name)
	return name == "localhost" || ip != nil && ip.IsLoopback()
}

func isDots(s string) bool { return s == "." || s == ".." }

// baseURL reports whether lg can append API paths to s, and shows s with any password hidden.
func baseURL(s string) (u *url.URL, shown string, ok bool) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, s, false
	}
	if u.User != nil {
		return nil, u.Redacted(), false
	}
	return u, s, (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && !strings.ContainsAny(s, "?#")
}
