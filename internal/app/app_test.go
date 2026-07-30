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
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Fatalf("%s Content-Type = %q, want HTML", path, got)
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
