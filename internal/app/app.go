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

	"nvidia-router/internal/accesskey"
	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
	"nvidia-router/internal/httpapi"
	"nvidia-router/internal/httpapi/health"
	v1 "nvidia-router/internal/httpapi/v1"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/nvidiakey"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
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
	Pool            *pool.Pool
	RuntimeSettings runtimeconfig.Provider
	Server          *Server

	db       *sql.DB
	handler  http.Handler
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
	keys, err := initialize(ctx, db, resolved)
	if err != nil {
		return nil, closeAfterInitializationError(db, err)
	}
	settings, err := runtimeconfig.New(ctx, db)
	if err != nil {
		return nil, closeAfterInitializationError(db, fmt.Errorf("initialize runtime settings store: %w", err))
	}
	keyRepository := nvidiakey.NewRepository(db)
	keySnapshots, err := keyRepository.ListSnapshots(ctx)
	if err != nil {
		return nil, closeAfterInitializationError(db, fmt.Errorf("load NVIDIA key scheduling snapshots: %w", err))
	}
	modelRepository := modelcatalog.NewRepository(db)
	modelBlocks, err := modelRepository.ListBlocks(ctx)
	if err != nil {
		return nil, closeAfterInitializationError(db, fmt.Errorf("load NVIDIA key model blocks: %w", err))
	}
	keyPool := pool.New(settings, resolved.Clock)
	keyPool.LoadSnapshot(keySnapshots, modelBlocks)
	descriptor, err := nvidiaDescriptor(resolved.Config)
	if err != nil {
		return nil, closeAfterInitializationError(db, err)
	}
	nvidiaClient, err := nvidia.NewClient(resolved.NVIDIAHTTPClient, descriptor)
	if err != nil {
		return nil, closeAfterInitializationError(db, fmt.Errorf("initialize NVIDIA client: %w", err))
	}
	nvidiaKeys := nvidiakey.NewService(keyRepository, keys, nvidiaClient, resolved.Clock)
	models := modelcatalog.NewService(modelRepository, nvidiaKeys, nvidiaClient, descriptor, resolved.Clock)
	accessKeys := accesskey.NewService(accesskey.NewRepository(db), keys, resolved.Clock)
	attempts := router.NewAttempt(settings, keyPool, nvidiaKeys, nvidiaKeys, keyPool, resolved.Clock)
	chat := httpapi.DataMiddleware(accessKeys, v1.NewChat(models, attempts, nvidiaClient))
	responses := httpapi.DataMiddleware(accessKeys, v1.NewResponses(models, attempts, nvidiaClient))
	embeddings := httpapi.DataMiddleware(accessKeys, v1.NewEmbeddings(models, attempts, nvidiaClient))
	audio := httpapi.DataMiddleware(accessKeys, v1.NewAudio(models, attempts, nvidiaClient))
	speech := httpapi.DataMiddleware(accessKeys, v1.NewSpeech(models, attempts, nvidiaClient))
	modelList := httpapi.DataMiddleware(accessKeys, v1.NewModels(models))

	resolved.DB = db
	app := &App{Dependencies: resolved, db: db, Pool: keyPool, RuntimeSettings: settings}
	app.handler = httpapi.NewRouter(health.New(db, keys, app.shutting.Load), chat, responses, embeddings, audio, speech, modelList)
	app.Server = NewServer(resolved.Config.ListenAddress, app.handler, settings, func() { app.shutting.Store(true) })
	return app, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func nvidiaDescriptor(cfg config.Config) (nvidia.Descriptor, error) {
	descriptor := nvidia.DefaultDescriptor()
	if cfg.NVIDIABaseURL == nil {
		return descriptor, nil
	}
	rewritten, err := descriptor.WithBaseURL(cfg.NVIDIABaseURL)
	if err != nil {
		return nvidia.Descriptor{}, fmt.Errorf("configure NVIDIA endpoints: %w", err)
	}
	return rewritten, nil
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

func initialize(ctx context.Context, db *sql.DB, dependencies Dependencies) (*crypto.KeySet, error) {
	keys, err := crypto.New(dependencies.Config.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("create crypto key set: %w", err)
	}
	if err := keys.EnsureSentinel(ctx, db); err != nil {
		return nil, fmt.Errorf("ensure crypto sentinel: %w", err)
	}
	if err := adminauth.NewRepository(db, dependencies.Clock).EnsureAdmin(ctx); err != nil {
		return nil, fmt.Errorf("initialize administrator: %w", err)
	}
	return keys, nil
}

func closeAfterInitializationError(db *sql.DB, operationErr error) error {
	if closeErr := db.Close(); closeErr != nil {
		return fmt.Errorf("initialize application and close database: %w", errors.Join(operationErr, closeErr))
	}
	return operationErr
}

func (a *App) Serve(ctx context.Context) error {
	err := a.Server.ListenAndServe(ctx)
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
