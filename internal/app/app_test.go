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
	"strings"
	"testing"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/database"
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
	response := httptestGet(t, app.Handler, "/health/live")
	if response.Code != http.StatusOK {
		t.Fatalf("live status = %d", response.Code)
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
