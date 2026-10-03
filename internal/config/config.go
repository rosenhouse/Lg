package config

import (
	"fmt"
	"os"
	"regexp"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Host   string `yaml:"host"`
	Repo   string `yaml:"repo"`
	APIURL string `yaml:"api_url"`
}

var ownerName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, Error(err.Error())
	}
	cfg := Config{Host: "github.com"}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, Error(fmt.Sprintf("%s: %s", path, err))
	}
	if !ownerName.MatchString(cfg.Repo) {
		return Config{}, Error(fmt.Sprintf("repo must be owner/name: %q", cfg.Repo))
	}
	return cfg, nil
}
