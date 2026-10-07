package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rosenhouse/lg/internal/layout"
)

type Config struct {
	Host   string `yaml:"host"`
	Repo   string `yaml:"repo"`
	APIURL string `yaml:"api_url"`

	SyncInterval     Duration `yaml:"sync_interval"`
	Backfill         Duration `yaml:"backfill"`
	Retention        Duration `yaml:"retention"`
	DiskCap          Bytes    `yaml:"disk_cap"`
	LogGrace         Duration `yaml:"log_grace"`
	ArtifactMaxBytes Bytes    `yaml:"artifact_max_bytes"`
}

// Bytes is a size in bytes: a whole number with an optional unit. KB, MB,
// GB and TB are decimal; KiB, MiB, GiB and TiB are binary.
type Bytes int64

var (
	size      = regexp.MustCompile(`^([0-9]+)([KMGT]i?B|B)?$`)
	sizeUnits = map[string]int64{
		"": 1, "B": 1,
		"KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
		"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
	}
)

func (b *Bytes) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: want a size such as 500MB, not %s", node.Line, node.ShortTag())
	}
	parsed, err := ParseBytes(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*b = parsed
	return nil
}

// ParseBytes parses a size such as 500MB.
func ParseBytes(s string) (Bytes, error) {
	m := size.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("want a size such as 500MB, not %q", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	unit := sizeUnits[m[2]]
	if err != nil || n > math.MaxInt64/unit {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	return Bytes(n * unit), nil
}

// Duration is a Go duration such as 1h or 0s, a whole number of days such
// as 7d, or a bare 0.
type Duration time.Duration

const day = 24 * time.Hour

var days = regexp.MustCompile(`^[0-9]+d$`)

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: want a duration such as 1h, not %s", node.Line, node.ShortTag())
	}
	parsed, err := ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*d = Duration(parsed)
	return nil
}

// ParseDuration parses a Go duration such as 1h or 0s, a whole number of
// days such as 7d, or a bare 0.
func ParseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if !days.MatchString(s) || err != nil || n > int64(math.MaxInt64/day) {
			return 0, fmt.Errorf("want a duration such as 7d or 36h, not %q", s)
		}
		return time.Duration(n) * day, nil
	}
	return time.ParseDuration(s)
}

// String prints whole days as days, and otherwise as Go does without zero parts.
func (d Duration) String() string {
	if d != 0 && time.Duration(d)%day == 0 {
		return strconv.FormatInt(int64(time.Duration(d)/day), 10) + "d"
	}
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

func Defaults() Config {
	return Config{
		Host:             "github.com",
		SyncInterval:     Duration(10 * time.Minute),
		Backfill:         Duration(7 * day),
		Retention:        Duration(90 * day),
		DiskCap:          50_000_000_000,
		ArtifactMaxBytes: 500_000_000,
		LogGrace:         Duration(time.Hour),
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, Error(path + " does not exist; run `lg init --repo owner/name`")
	}
	if err != nil {
		return Config{}, Error(err.Error())
	}
	cfg := Defaults()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, Error(fmt.Sprintf("%s: %s", path, describe(err)))
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Config{}, Error(path + ": more than one YAML document")
	}
	cfg.Host = strings.ToLower(cfg.Host)
	if err := Validate(cfg); err != nil {
		return Config{}, Error(fmt.Sprintf("%s: %s", path, err))
	}
	return cfg, nil
}

var unknownField = regexp.MustCompile(`^line (\d+): field (.+) not found in type \S+$`)

// describe names each unknown key in a yaml.TypeError, and gives any other error as is.
func describe(err error) string {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return err.Error()
	}
	messages := make([]string, len(typeErr.Errors))
	for i, message := range typeErr.Errors {
		messages[i] = unknownField.ReplaceAllString(message, `line $1: unknown key "$2"`)
	}
	return strings.Join(messages, "; ")
}

// Validate returns an Error naming the first key whose value lg cannot use.
func Validate(cfg Config) error {
	switch {
	case !hostName.MatchString(cfg.Host):
		return Error(fmt.Sprintf("host must be a host name: %q", cfg.Host))
	case !layout.IsRepo(cfg.Repo):
		return Error(fmt.Sprintf("repo must be owner/name: %q", cfg.Repo))
	case cfg.SyncInterval < Duration(time.Minute):
		return Error(fmt.Sprintf("sync_interval must be at least 1m: %s", cfg.SyncInterval))
	case cfg.Backfill <= 0:
		return Error(fmt.Sprintf("backfill must be positive: %s", cfg.Backfill))
	case cfg.Retention <= 0:
		return Error(fmt.Sprintf("retention must be positive: %s", cfg.Retention))
	case cfg.Backfill > cfg.Retention:
		return Error(fmt.Sprintf("backfill must not exceed retention: %s > %s", cfg.Backfill, cfg.Retention))
	case cfg.LogGrace < 0:
		return Error(fmt.Sprintf("log_grace must not be negative: %s", cfg.LogGrace))
	case cfg.DiskCap < 1:
		return Error("disk_cap must be at least 1B")
	case cfg.ArtifactMaxBytes < 1:
		return Error("artifact_max_bytes must be at least 1B")
	case cfg.APIURL != "":
		return checkAPIURL(cfg.APIURL, cfg.Host)
	}
	return nil
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
