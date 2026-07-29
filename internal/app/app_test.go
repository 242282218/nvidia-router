package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	app, err := New(context.Background(), Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app == nil {
		t.Fatal("expected app")
	}
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
	if stdout != "Usage: nvidia-router [--help]\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunCLIRejectsInvalidArgumentsProcess(t *testing.T) {
	for _, args := range [][]string{{"serve"}, {"--unknown"}} {
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
