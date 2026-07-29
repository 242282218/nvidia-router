package app

import "context"

type Dependencies struct{}

type App struct{}

func New(_ context.Context, _ Dependencies) (*App, error) {
	return &App{}, nil
}
