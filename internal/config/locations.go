package config

type Roots struct {
	Home string
}

func Locations(env map[string]string) (Roots, error) { return Roots{}, nil }
