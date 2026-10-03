package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
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
	if !ownerName.MatchString(cfg.Repo) {
		return Config{}, Error(fmt.Sprintf("repo must be owner/name: %q", cfg.Repo))
	}
	if cfg.APIURL != "" && !httpURL(cfg.APIURL) {
		return Config{}, Error(fmt.Sprintf("api_url must be an http or https URL: %q", cfg.APIURL))
	}
	return cfg, nil
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
