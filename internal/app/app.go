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
	"time"

	"nvidia-router/internal/accesskey"
	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
	"nvidia-router/internal/httpapi"
	adminapi "nvidia-router/internal/httpapi/admin"
	"nvidia-router/internal/httpapi/health"
	metricsapi "nvidia-router/internal/httpapi/metrics"
	v1 "nvidia-router/internal/httpapi/v1"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/nvidiakey"
	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/router"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
	webui "nvidia-router/internal/web"
	"nvidia-router/internal/xkproxy"
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
	RuntimeSettings *runtimeconfig.Store
	Server          *Server
	proxy           *xkproxy.Switcher
	proxySettings   *xkproxy.SettingsService
	nvidiaClient    *nvidia.Client

	db               *sql.DB
	dbReader         *sql.DB
	handler          http.Handler
	requestRecorder  *observability.BufferRecorder
	healthChecker    *nvidiakey.HealthChecker
	shutting         atomic.Bool
	cleanupCancel    context.CancelFunc
	cleanupDone      chan struct{}
	recorderCancel   context.CancelFunc
	recorderDone     chan struct{}
	healthCancel     context.CancelFunc
	healthDone       chan struct{}
	rootCancel       context.CancelFunc
	shutdownOnce     sync.Once
	shutdownGrace    time.Duration
	shutdownTimer    *time.Timer
	shutdownDeadline time.Time
	close            sync.Once
	closeErr         error
}

func New(ctx context.Context, dependencies Dependencies) (*App, error) {
	resolved, err := resolveDependencies(dependencies)
	if err != nil {
		return nil, err
	}
	db, reader, err := openDatabase(resolved)
	if err != nil {
		return nil, err
	}
	keys, err := initialize(ctx, db, resolved)
	if err != nil {
		return nil, closeAfterInitializationError(db, reader, err)
	}
	settings, err := runtimeconfig.New(ctx, db)
	if err != nil {
		return nil, closeAfterInitializationError(db, reader, fmt.Errorf("initialize runtime settings store: %w", err))
	}
	keyRepository := nvidiakey.NewRepository(db)
	keySnapshots, err := keyRepository.ListSnapshots(ctx)
	if err != nil {
		return nil, closeAfterInitializationError(db, reader, fmt.Errorf("load NVIDIA key scheduling snapshots: %w", err))
	}
	modelRepository := modelcatalog.NewRepository(db)
	modelBlocks, err := modelRepository.ListBlocks(ctx)
	if err != nil {
		return nil, closeAfterInitializationError(db, reader, fmt.Errorf("load NVIDIA key model blocks: %w", err))
	}
	keyPool := pool.New(settings, resolved.Clock)
	keyPool.LoadSnapshot(keySnapshots, modelBlocks)
	descriptor, err := nvidiaDescriptor(resolved.Config)
	if err != nil {
		return nil, closeAfterInitializationError(db, reader, err)
	}
	base := resolved.NVIDIAHTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	baseTransport, ok := base.(*http.Transport)
	if !ok {
		return nil, closeAfterInitializationError(db, reader, errors.New("initialize proxy manager: HTTP transport is required"))
	}
	proxySettings, err := xkproxy.NewSettingsService(ctx, db, keys, xkproxy.EnvironmentConfig{
		URL: resolved.Config.XKProxyURL, AuthKey: resolved.Config.XKProxyAuthKey,
	}, baseTransport, resolved.Logger)
	if err != nil {
		return nil, closeAfterInitializationError(db, reader, fmt.Errorf("initialize proxy settings: %w", err))
	}
	proxy := proxySettings.Switcher()
	nvidiaClient, err := nvidia.NewClient(resolved.NVIDIAHTTPClient, descriptor, settings, proxy)
	if err != nil {
		proxySettings.Close()
		return nil, closeAfterInitializationError(db, reader, fmt.Errorf("initialize NVIDIA client: %w", err))
	}
	nvidiaKeys := nvidiakey.NewService(keyRepository, keys, nvidiaClient, resolved.Clock)
	healthChecker := nvidiakey.NewHealthChecker(keyRepository, resolved.Clock, nvidiakey.HealthCheckerOptions{
		Logger: resolved.Logger,
	})
	healthChecker.WireProbe(nvidiaKeys.ProbeHealth)
	healthChecker.WireWriter(nvidiaKeys)
	// Mirror DB recovery into pool state so a key the checker revives is
	// immediately acquirable without waiting for the next restart.
	healthChecker.WireSync(keyPool.ApplySuccess)
	models := modelcatalog.NewService(modelRepository, nvidiaKeys, nvidiaClient, descriptor, resolved.Clock)
	accessKeys := accesskey.NewService(accesskey.NewRepository(db).WithReader(reader), keys, resolved.Clock)
	adminRepository := adminauth.NewRepository(db, resolved.Clock)
	originPolicy := adminauth.OriginPolicy{ExternalOrigin: resolved.Config.AdminExternalOrigin, TrustedProxies: resolved.Config.TrustedProxyCIDRs}
	adminSecurity := adminapi.NewAuth(adminRepository, adminauth.NewSessionService(db, resolved.Clock, keys, resolved.Config.AdminSecureCookie), adminauth.NewLoginLimiter(resolved.Clock), originPolicy)
	adminManagement := adminapi.NewManagement(
		adminapi.NewNVIDIAKeys(nvidiaKeys, keyPool),
		adminapi.NewAccessKeys(accessKeys),
		adminapi.NewModels(models, nvidiaKeys, keyPool),
		adminapi.NewProxyPool(proxySettings),
	)
	attempts := router.NewAttempt(settings, keyPool, nvidiaKeys, nvidiaKeys, keyPool, resolved.Clock)
	observabilityRepository := observability.NewRepository(db).WithReader(reader)
	// Wrap the repository with a buffering recorder so request_logs writes
	// move off the hot path: per-request Record only enqueues, a background
	// flusher persists batches in a single SQLite transaction (audit #25).
	requestRecorder := observability.NewBufferRecorder(observabilityRepository, resolved.Clock, observability.BufferOptions{
		Logger: resolved.Logger,
	})
	observe := func(next http.Handler) http.Handler {
		guarded := httpapi.DataMiddleware(accessKeys, next)
		return observedHandler(requestRecorder, resolved.Clock, resolved.Logger, guarded)
	}
	chat := observe(v1.NewChat(models, attempts, nvidiaClient))
	responses := observe(v1.NewResponses(models, attempts, nvidiaClient))
	embeddings := observe(v1.NewEmbeddings(models, attempts, nvidiaClient))
	audio := observe(v1.NewAudio(models, attempts, nvidiaClient, resolved.Config.TempDir))
	speech := observe(v1.NewSpeech(models, attempts, nvidiaClient))
	modelList := observe(v1.NewModels(models))
	frontend, err := webui.NewEmbeddedHandler()
	if err != nil {
		nvidiaClient.Close()
		proxySettings.Close()
		return nil, closeAfterInitializationError(db, reader, fmt.Errorf("initialize embedded frontend: %w", err))
	}

	resolved.DB = db
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	recorderCtx, recorderCancel := context.WithCancel(context.Background())
	recorderDone := make(chan struct{})
	healthCtx, healthCancel := context.WithCancel(context.Background())
	healthDone := make(chan struct{})
	rootCtx, rootCancel := context.WithCancel(context.Background())
	app := &App{
		Dependencies: resolved, db: db, dbReader: reader, Pool: keyPool, RuntimeSettings: settings,
		proxy: proxy, proxySettings: proxySettings, nvidiaClient: nvidiaClient,
		requestRecorder: requestRecorder, healthChecker: healthChecker,

		cleanupCancel: cleanupCancel, cleanupDone: cleanupDone,
		recorderCancel: recorderCancel, recorderDone: recorderDone,
		healthCancel: healthCancel, healthDone: healthDone,
		rootCancel: rootCancel,
	}
	unsupported := observe(v1.Unsupported)
	statsHandler := adminapi.NewStats(observabilityRepository, resolved.Clock)
	monitoringHandler := adminapi.NewMonitoring(observabilityRepository, resolved.Clock)
	metricsHandler := metricsapi.New(keyPool, observabilityRepository)
	app.handler = httpapi.RecoverMiddleware(resolved.Logger, shutdownMiddleware(app.shutting.Load, httpapi.NewRouter(
		health.New(db, keys, app.shutting.Load).WithReader(reader), chat, responses, embeddings, audio, speech, modelList, unsupported,
		adminSecurity, adminManagement, adminapi.NewSettings(settings), adminapi.NewRuntime(keyPool), frontend,
		statsHandler, monitoringHandler, metricsHandler,
	)))
	observabilityWorker := observability.NewCleanupWorker(observabilityRepository, resolved.Clock, resolved.Logger, settings)
	adminSessionWorker := adminauth.NewSessionCleanupWorker(adminRepository, resolved.Clock, resolved.Logger)
	var cleanupWorkers sync.WaitGroup
	cleanupWorkers.Add(2)
	go func() {
		defer cleanupWorkers.Done()
		observabilityWorker.Run(cleanupCtx)
	}()
	go func() {
		defer cleanupWorkers.Done()
		adminSessionWorker.Run(cleanupCtx)
	}()
	go func() {
		cleanupWorkers.Wait()
		close(cleanupDone)
	}()
	// Flusher pairs with the buffer recorder: it must outlive request serving
	// long enough to drain the in-memory queue, so it gets its own ctx that
	// shutdown.go cancels right before closing the DB.
	go func() {
		defer close(recorderDone)
		requestRecorder.Run(recorderCtx)
	}()
	// Health checker pairs with the request path: it independently probes
	// unhealthy keys and recovers valid ones so a user request doesn't pay the
	// first failure after a key recovers from cooldown.
	go func() {
		defer close(healthDone)
		healthChecker.Run(healthCtx)
	}()
	app.Server = NewServer(resolved.Config.ListenAddress, app.handler, settings, func() { app.beginShutdown(0) })
	app.Server.setRootContext(rootCtx)
	return app, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

// FlushObservability synchronously persists any buffered request-log records.
// Tests and admin surfaces use it to make buffered logs visible without
// waiting on the flusher's timer. Safe to call when the recorder is unstarted
// or already stopped (no-op).
func (a *App) FlushObservability(ctx context.Context) error {
	if a.requestRecorder == nil {
		return nil
	}
	return a.requestRecorder.ForceFlush(ctx)
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
	if dependencies.Config.ListenAddress == "" && dependencies.Config.DataDir == "" && dependencies.Config.TempDir == "" && dependencies.Config.MasterKey == ([32]byte{}) && dependencies.Config.InitialAdminPassword == "" && !dependencies.Config.AdminSecureCookie && dependencies.Config.AdminExternalOrigin == nil && len(dependencies.Config.TrustedProxyCIDRs) == 0 && dependencies.Config.NVIDIABaseURL == nil && dependencies.Config.XKProxyURL == nil && dependencies.Config.XKProxyAuthKey == "" {
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

// openDatabase returns the writer pool and, when this process owns the file, a
// separate read-only pool. An injected DB (tests, CLI) has no path to reopen,
// so the reader is nil there and repositories fall back to the writer.
func openDatabase(dependencies Dependencies) (*sql.DB, *sql.DB, error) {
	if dependencies.DB != nil {
		return dependencies.DB, nil, nil
	}
	if err := os.MkdirAll(dependencies.Config.DataDir, 0o750); err != nil {
		return nil, nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dependencies.Config.DataDir, routerDBFilename)
	db, err := database.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open router database: %w", err)
	}
	// Must follow Open: a read-only connection cannot create the WAL index, so
	// the writer has to have initialized it first.
	reader, err := database.OpenReader(path)
	if err != nil {
		return nil, nil, closeAfterInitializationError(db, nil, fmt.Errorf("open router database reader pool: %w", err))
	}
	return db, reader, nil
}

func initialize(ctx context.Context, db *sql.DB, dependencies Dependencies) (*crypto.KeySet, error) {
	activeVersion := dependencies.Config.MasterKeyVersion
	if activeVersion <= 0 {
		activeVersion = 1
	}
	keys, err := crypto.NewVersioned(activeVersion, dependencies.Config.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("create crypto key set: %w", err)
	}
	if dependencies.Config.LegacyMasterKey != nil {
		legacyVersion := dependencies.Config.LegacyMasterKeyVersion
		if legacyVersion <= 0 {
			legacyVersion = 1
		}
		keys, err = keys.WithLegacyMasterKey(legacyVersion, *dependencies.Config.LegacyMasterKey)
		if err != nil {
			return nil, fmt.Errorf("add legacy crypto key: %w", err)
		}
	}
	if err := keys.EnsureSentinel(ctx, db); err != nil {
		return nil, fmt.Errorf("ensure crypto sentinel: %w", err)
	}
	if err := adminauth.NewRepository(db, dependencies.Clock).EnsureAdmin(ctx, dependencies.Config.InitialAdminPassword); err != nil {
		return nil, fmt.Errorf("initialize administrator: %w", err)
	}
	return keys, nil
}

func closeAfterInitializationError(db *sql.DB, reader *sql.DB, operationErr error) error {
	closeErrs := []error{operationErr}
	if reader != nil {
		closeErrs = append(closeErrs, reader.Close())
	}
	closeErrs = append(closeErrs, db.Close())
	if joined := errors.Join(closeErrs[1:]...); joined != nil {
		return fmt.Errorf("initialize application and close database: %w", errors.Join(operationErr, joined))
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
	a.beginShutdown(0)
	a.close.Do(func() {
		a.closeErr = a.finishShutdown()
	})
	if a.closeErr != nil {
		return fmt.Errorf("close router database: %w", a.closeErr)
	}
	return nil
}
