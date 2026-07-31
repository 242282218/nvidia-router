package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/database"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/nvidiakey"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

func TestNew(t *testing.T) {
	db := openAppDatabase(t)
	defer db.Close()
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{DataDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app == nil {
		t.Fatal("expected app")
	}
	if app.Dependencies.DB != db {
		t.Fatal("App did not retain the injected database dependency")
	}
	response := httptestGet(t, app.Handler(), "/health/live")
	if response.Code != http.StatusOK {
		t.Fatalf("live status = %d", response.Code)
	}
}

func TestNewCreatesAndClosesProxyManagerWhenConfigured(t *testing.T) {
	proxyAPI := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "192.0.2.10:8000")
	}))
	t.Cleanup(proxyAPI.Close)
	proxyURL, err := url.Parse(proxyAPI.URL + "?qty=1")
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	db := openAppDatabase(t)
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{
			DataDir:            t.TempDir(),
			MasterKey:          [32]byte{1},
			XKProxyAPIURL:      proxyURL,
			XKProxyTTL:         3 * time.Minute,
			XKProxyRenewBefore: 15 * time.Second,
		},
		DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
	})
	if err != nil {
		db.Close()
		t.Fatalf("New: %v", err)
	}
	if app.proxy == nil {
		t.Fatal("proxy manager was not created")
	}
	if err := app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err = app.proxy.Acquire(context.Background(), runtimeconfig.Snapshot{})
	var proxyErr *xkproxy.Error
	if !errors.As(err, &proxyErr) {
		t.Fatalf("Acquire after Close error = %T %v, want manager closed", err, err)
	}
}

func TestNewServesEmbeddedFrontendAndKeepsAPIPrefixesOutOfSPA(t *testing.T) {
	db := openAppDatabase(t)
	defer db.Close()
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{DataDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, path := range []string{"/", "/admin/keys", "/unknown/deep-link"} {
		response := httptestGet(t, app.Handler(), path)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "NVIDIA API Router") {
			t.Fatalf("%s body = %q, want embedded index marker", path, response.Body.String())
		}
	}

	for _, path := range []string{"/v1", "/v1/unknown", "/admin/api", "/admin/api/unknown", "/health", "/health/unknown"} {
		response := httptestGet(t, app.Handler(), path)
		if strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s returned HTML: status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func TestNewRestoresPoolStateFromDatabase(t *testing.T) {
	db := openAppDatabase(t)
	defer db.Close()
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	keyRepository := nvidiakey.NewRepository(db)
	first, _, err := keyRepository.Create(context.Background(), []byte{1}, []byte{2}, []byte{3}, "key", "one", now)
	if err != nil {
		t.Fatalf("create first key: %v", err)
	}
	second, _, err := keyRepository.Create(context.Background(), []byte{4}, []byte{5}, []byte{6}, "key", "two", now)
	if err != nil {
		t.Fatalf("create second key: %v", err)
	}
	if _, err := db.Exec("UPDATE nvidia_keys SET enabled = 0 WHERE id = ?", first.ID); err != nil {
		t.Fatalf("disable first key: %v", err)
	}
	modelRepository := modelcatalog.NewRepository(db)
	if err := modelRepository.SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: "test-model", UpstreamID: "test-model", DisplayName: "Test Model", Kind: modelcatalog.KindChat, Enabled: true, ReasoningWireFormat: "none",
	}}, now); err != nil {
		t.Fatalf("save test model: %v", err)
	}
	model, err := modelRepository.ResolveEnabled(context.Background(), "test-model")
	if err != nil {
		t.Fatalf("resolve test model: %v", err)
	}
	if err := modelRepository.BlockKeyModel(context.Background(), second.ID, model.ID, "model_unsupported", nil, now); err != nil {
		t.Fatalf("block second key model: %v", err)
	}

	app, err := New(context.Background(), Dependencies{
		Config: config.Config{DataDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app.Pool == nil {
		t.Fatal("New did not initialize Pool")
	}
	poolKeys := reflect.ValueOf(app.Pool).Elem().FieldByName("keys")
	if poolKeys.Len() != 2 {
		t.Fatalf("restored pool key count = %d, want 2", poolKeys.Len())
	}
	firstState := poolKeys.MapIndex(reflect.ValueOf(first.ID)).Elem()
	if firstState.FieldByName("snapshot").FieldByName("Enabled").Bool() {
		t.Fatal("restored first key is enabled, want disabled")
	}
	secondState := poolKeys.MapIndex(reflect.ValueOf(second.ID)).Elem()
	blocksValue := secondState.FieldByName("blocks")
	if !blocksValue.MapIndex(reflect.ValueOf(model.ID)).IsValid() {
		t.Fatal("restored second key is missing its model block")
	}

	keys, err := keyRepository.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("list key snapshots: %v", err)
	}
	blocks, err := modelcatalog.NewRepository(db).ListBlocks(context.Background())
	if err != nil {
		t.Fatalf("list model blocks: %v", err)
	}
	if len(keys) != 2 || keys[0].ID != first.ID || keys[1].ID != second.ID {
		t.Fatalf("key snapshots = %#v", keys)
	}
	if len(blocks) != 1 || blocks[0] != (keystate.ModelBlock{KeyID: second.ID, ModelID: model.ID}) {
		t.Fatalf("model blocks = %#v", blocks)
	}
}

func TestNewClosesDatabaseWhenInitializationFails(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Exec("DROP TABLE runtime_settings"); err != nil {
		t.Fatalf("drop runtime settings: %v", err)
	}

	_, err = New(context.Background(), Dependencies{
		Config: config.Config{DataDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db,
	})
	if err == nil {
		t.Fatal("New succeeded without runtime settings")
	}
	if err := db.Ping(); err == nil {
		t.Fatal("New did not close an injected database after initialization failure")
	}
}

func openAppDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return db
}

func completeInitialPasswordChange(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("UPDATE admins SET must_change_password = 0 WHERE id = 1"); err != nil {
		t.Fatalf("complete initial password change: %v", err)
	}
}

func httptestGet(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}

func TestRunCLIPropagatesUsageWriteError(t *testing.T) {
	writeErr := errors.New("write usage")

	err := runCLI([]string{"--help"}, errorWriter{err: writeErr}, io.Discard)

	if !errors.Is(err, writeErr) {
		t.Fatalf("runCLI error = %v, want %v", err, writeErr)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestRunCLIProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CLI_HELPER") != "1" {
		return
	}

	for index, arg := range os.Args {
		if arg == "--" {
			RunCLI(os.Args[index+1:])
			os.Exit(0)
		}
	}

	t.Fatal("missing CLI argument separator")
}

func TestRunCLIHelpProcess(t *testing.T) {
	stdout, stderr, err := runCLIProcess(t, "--help")

	if err != nil {
		t.Fatalf("RunCLI --help: %v\nstderr: %s", err, stderr)
	}
	want := "Usage:\n" +
		"  nvidia-router [--help]\n" +
		"  nvidia-router serve\n" +
		"  nvidia-router admin reset-password --password <new>\n" +
		"  nvidia-router db backup --output <path>\n"
	if stdout != want {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunCLIRejectsInvalidArgumentsProcess(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}} {
		assertRunCLIFails(t, args...)
	}
}

func TestRunCLIRejectsHelpAliasesProcess(t *testing.T) {
	for _, args := range [][]string{
		{"-h"},
		{"--h"},
		{"-help"},
		{"--help=value"},
		{"--help", "extra"},
	} {
		assertRunCLIFails(t, args...)
	}
}

func assertRunCLIFails(t *testing.T, args ...string) {
	t.Helper()
	t.Run(strings.Join(args, " "), func(t *testing.T) {
		_, stderr, err := runCLIProcess(t, args...)

		if err == nil {
			t.Fatal("RunCLI succeeded, want non-zero exit")
		}
		if stderr == "" {
			t.Fatal("stderr is empty")
		}
	})
}

func runCLIProcess(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	command := exec.Command(os.Args[0], "-test.run=^TestRunCLIProcess$", "--")
	command.Args = append(command.Args, args...)
	command.Env = append(os.Environ(), "GO_WANT_CLI_HELPER=1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()

	return stdout.String(), stderr.String(), err
}

func TestNewDoesNotCreateProxyManagerWhenDisabled(t *testing.T) {
	db := openAppDatabase(t)
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{DataDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.RealClock{},
	})
	if err != nil {
		db.Close()
		t.Fatalf("New: %v", err)
	}
	if app.proxy != nil {
		t.Fatal("proxy manager was created although XKProxyAPIURL is unset")
	}
	if err := app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewProxyManagerUsesDefaultTransportWhenNil(t *testing.T) {
	proxyAPI := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "192.0.2.10:8000")
	}))
	t.Cleanup(proxyAPI.Close)
	proxyURL, err := url.Parse(proxyAPI.URL + "?qty=1")
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	db := openAppDatabase(t)
	defer db.Close()
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{
			DataDir:            t.TempDir(),
			MasterKey:          [32]byte{1},
			XKProxyAPIURL:      proxyURL,
			XKProxyTTL:         3 * time.Minute,
			XKProxyRenewBefore: 15 * time.Second,
		},
		DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app.proxy == nil {
		t.Fatal("proxy manager was not created")
	}
	// No NVIDIAHTTPClient was injected, so resolveDependencies supplies
	// http.DefaultClient, whose Transport field is nil. New must fall back to
	// http.DefaultTransport when wiring the proxy manager base transport;
	// xkproxy.New requires a concrete *http.Transport, so a missing fallback
	// would have made New fail with "HTTP transport is required". Reaching
	// this point with a live proxy manager therefore proves the fallback, and
	// the manager's base transport must be http.DefaultTransport itself.
	base := reflect.ValueOf(app.proxy).Elem().FieldByName("base")
	if base.Kind() != reflect.Pointer || base.IsNil() {
		t.Fatalf("proxy manager base transport = %v, want a non-nil *http.Transport", base.Kind())
	}
	if base.Pointer() != reflect.ValueOf(http.DefaultTransport).Pointer() {
		t.Fatalf("proxy manager base transport pointer = %#x, want http.DefaultTransport %#x", base.Pointer(), reflect.ValueOf(http.DefaultTransport).Pointer())
	}
}

func TestNewFailsWhenProxyBaseTransportIsCustomRoundTripper(t *testing.T) {
	db := openAppDatabase(t)
	defer db.Close()
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{
			DataDir:            t.TempDir(),
			MasterKey:          [32]byte{1},
			XKProxyAPIURL:      mustURL(t, "http://192.0.2.99/tools/XApi.ashx?qty=1&apikey=secret&sign=secret"),
			XKProxyTTL:         3 * time.Minute,
			XKProxyRenewBefore: 15 * time.Second,
		},
		DB:               db,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:            clock.RealClock{},
		NVIDIAHTTPClient: &http.Client{Transport: customRoundTripper{}},
	})
	if err == nil {
		t.Fatal("New succeeded with a custom non-*http.Transport base RoundTripper")
	}
	if app != nil {
		t.Fatal("New returned a non-nil app alongside an error")
	}
	if !strings.Contains(err.Error(), "transport") {
		t.Fatalf("New error = %v, want mention of transport", err)
	}
	for _, leaked := range []string{"192.0.2.99", "apikey", "sign", "/tools/XApi.ashx", "XApi"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("New error leaked %q: %v", leaked, err)
		}
	}
}

func TestAppCloseIsIdempotent(t *testing.T) {
	proxyAPI := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "192.0.2.10:8000")
	}))
	t.Cleanup(proxyAPI.Close)
	proxyURL, err := url.Parse(proxyAPI.URL + "?qty=1")
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	db := openAppDatabase(t)
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{
			DataDir:            t.TempDir(),
			MasterKey:          [32]byte{1},
			XKProxyAPIURL:      proxyURL,
			XKProxyTTL:         3 * time.Minute,
			XKProxyRenewBefore: 15 * time.Second,
		},
		DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
	})
	if err != nil {
		db.Close()
		t.Fatalf("New: %v", err)
	}
	// App.Close must be safe to call repeatedly: the proxy manager Close
	// inside is guarded by the manager's own closed flag, and App's
	// shutdown work runs only once (spec 14.6).
	if err := app.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("third Close: %v", err)
	}
	// The proxy manager itself must also tolerate repeated Close calls.
	app.proxy.Close()
	app.proxy.Close()
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}

// customRoundTripper is a minimal http.RoundTripper that is not a
// *http.Transport, used to verify New rejects a proxy base transport of the
// wrong concrete type.
type customRoundTripper struct{}

func (customRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unused")
}
