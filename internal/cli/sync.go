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
	if err := store.Init(roots.Store); err != nil {
		return err
	}
	s, err := store.Open(roots.Store)
	if err != nil {
		return err
	}
	m := mirror.Mirror{
		GitHub: github.NewHTTP(&http.Client{}, github.BaseURL(cfg.Host, cfg.APIURL), cfg.Repo),
		Store:  s,
		Data:   roots.Data,
		Host:   cfg.Host,
		Repo:   cfg.Repo,
	}
	return m.Cycle(context.Background())
}
