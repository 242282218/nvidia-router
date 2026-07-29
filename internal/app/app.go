package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
	"nvidia-router/internal/httpapi"
	"nvidia-router/internal/httpapi/health"
	"nvidia-router/internal/runtimeconfig"
)

type Dependencies struct {
	Config           config.Config
	DB               *sql.DB
	Logger           *slog.Logger
	Clock            clock.Clock
	NVIDIAHTTPClient *http.Client
}

type App struct {
	Dependencies    Dependencies
	Handler         http.Handler
	RuntimeSettings runtimeconfig.Provider
	Server          *Server

	db       *sql.DB
	shutting atomic.Bool
	close    sync.Once
	closeErr error
}

func New(ctx context.Context, dependencies Dependencies) (*App, error) {
	resolved, err := resolveDependencies(dependencies)
	if err != nil {
		return nil, err
	}
	db, err := openDatabase(resolved)
	if err != nil {
		return nil, err
	}
	if err := initialize(ctx, db, resolved); err != nil {
		return nil, closeAfterInitializationError(db, err)
	}
	settings, err := runtimeconfig.New(ctx, db)
	if err != nil {
		return nil, closeAfterInitializationError(db, fmt.Errorf("initialize runtime settings store: %w", err))
	}

	resolved.DB = db
	app := &App{Dependencies: resolved, db: db, RuntimeSettings: settings}
	app.Handler = httpapi.NewRouter(health.New(db, app.shutting.Load))
	app.Server = NewServer(resolved.Config.ListenAddress, app.Handler, settings)
	return app, nil
}

func resolveDependencies(dependencies Dependencies) (Dependencies, error) {
	if dependencies.Config == (config.Config{}) {
		loaded, err := config.LoadFromEnv(config.LoadOptions{})
		if err != nil {
			return Dependencies{}, fmt.Errorf("load configuration: %w", err)
		}
		dependencies.Config = loaded
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default()
	}
	if dependencies.Clock == nil {
		dependencies.Clock = clock.RealClock{}
	}
	if dependencies.NVIDIAHTTPClient == nil {
		dependencies.NVIDIAHTTPClient = http.DefaultClient
	}
	return dependencies, nil
}

func openDatabase(dependencies Dependencies) (*sql.DB, error) {
	if dependencies.DB != nil {
		return dependencies.DB, nil
	}
	if err := os.MkdirAll(dependencies.Config.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := database.Open(filepath.Join(dependencies.Config.DataDir, routerDBFilename))
	if err != nil {
		return nil, fmt.Errorf("open router database: %w", err)
	}
	return db, nil
}

func initialize(ctx context.Context, db *sql.DB, dependencies Dependencies) error {
	keys, err := crypto.New(dependencies.Config.MasterKey)
	if err != nil {
		return fmt.Errorf("create crypto key set: %w", err)
	}
	if err := keys.EnsureSentinel(ctx, db); err != nil {
		return fmt.Errorf("ensure crypto sentinel: %w", err)
	}
	if err := adminauth.NewRepository(db, dependencies.Clock).EnsureAdmin(ctx); err != nil {
		return fmt.Errorf("initialize administrator: %w", err)
	}
	return nil
}

func closeAfterInitializationError(db *sql.DB, operationErr error) error {
	if closeErr := db.Close(); closeErr != nil {
		return fmt.Errorf("initialize application and close database: %w", errors.Join(operationErr, closeErr))
	}
	return operationErr
}

func (a *App) Serve(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			a.shutting.Store(true)
		case <-done:
		}
	}()
	err := a.Server.ListenAndServe(ctx)
	close(done)
	if closeErr := a.Close(); closeErr != nil {
		return fmt.Errorf("serve application: %w", errors.Join(err, closeErr))
	}
	if err != nil {
		return fmt.Errorf("serve application: %w", err)
	}
	return nil
}

func (a *App) Close() error {
	a.shutting.Store(true)
	a.close.Do(func() {
		a.closeErr = a.db.Close()
	})
	if a.closeErr != nil {
		return fmt.Errorf("close router database: %w", a.closeErr)
	}
	return nil
}
