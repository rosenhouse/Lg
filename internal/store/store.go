// Package store stages units in tmp/ and publishes them into data/ whole.
package store

import "io"

type Store struct{}

func New(string) *Store { return &Store{} }

type Unit struct{}

func (*Store) NewUnit() (*Unit, error) { return &Unit{}, nil }

func (*Store) Publish(*Unit, string) error { return nil }

func (*Unit) Create(string) (io.WriteCloser, error) { return nil, nil }

func (*Unit) WriteJSON(string, []byte) error { return nil }
