package cli

import (
	"context"
	"net/http"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/store"
)

type syncCmd struct{}

func (syncCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	file, err := config.File(deps.Env)
	if err != nil {
		return err
	}
	cfg, err := config.Load(file)
	if err != nil {
		return err
	}
	m := mirror.Mirror{
		GitHub: github.NewHTTP(&http.Client{}, github.BaseURL(cfg.Host, cfg.APIURL), cfg.Repo),
		Store:  store.New(roots.Tmp),
		Data:   roots.Data,
		Host:   cfg.Host,
	}
	return m.Cycle(context.Background())
}
