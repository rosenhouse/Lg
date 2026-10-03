package config

type Config struct {
	Host   string
	Repo   string
	APIURL string
}

func Load(string) (Config, error) { return Config{}, nil }
