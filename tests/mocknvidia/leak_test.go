package mocknvidia_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nvidia-router/internal/database"
	"nvidia-router/tests/mocknvidia"
)

func TestSecretsAndBodiesDoNotLeakIntoResponsesLogsOrSQLite(t *testing.T) {
	const (
		nvidiaSecret       = "nvapi-fixture-DO-NOT-LEAK-7e9c4b"
		requestBodyFixture = "private-request-body-fixture-4a28d1"
		upstreamFixture    = "private-upstream-error-fixture-05bd32"
		cookieValue        = "session=private-cookie-fixture-f08c11"
	)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	previousDefault := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previousDefault) })

	upstream := mocknvidia.New(mocknvidia.Script{
		Status: http.StatusInternalServerError,
		Body:   `{"error":{"message":"` + upstreamFixture + `"}}`,
	})
	harness := newAppHarnessWithOptions(t, harnessOptions{
		upstream: upstream, secrets: []string{nvidiaSecret}, logger: logger,
	})
	authorizationValue := "Bearer " + harness.accessToken
	requestBody := `{"model":"public-chat","messages":[{"role":"user","content":"` + requestBodyFixture + `"}]}`
	result := harness.doRequest(context.Background(), http.MethodPost, "/v1/chat/completions", requestBody, http.Header{
		"Cookie": []string{cookieValue},
	})
	if result.err != nil {
		t.Fatalf("perform leak fixture request: %v", result.err)
	}
	if result.status != http.StatusInternalServerError || !strings.Contains(result.body, `"code":"upstream_error"`) {
		t.Fatalf("response = %d %s", result.status, result.body)
	}

	backupPath := filepath.Join(filepath.Dir(harness.dbPath), "router-backup.db")
	if err := database.Backup(context.Background(), harness.db, backupPath); err != nil {
		t.Fatalf("backup database for leak scan: %v", err)
	}
	artifacts := map[string][]byte{
		"http_response": []byte(result.body),
		"slog":          logs.Bytes(),
	}
	for name, path := range map[string]string{
		"sqlite":     harness.dbPath,
		"sqlite_wal": harness.dbPath + "-wal",
		"backup":     backupPath,
	} {
		payload, err := os.ReadFile(path)
		if err != nil {
			if name == "sqlite_wal" && os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s artifact: %v", name, err)
		}
		artifacts[name] = payload
	}

	for artifactName, artifact := range artifacts {
		for label, secret := range map[string]string{
			"NVIDIA key":          nvidiaSecret,
			"Access Key":          harness.accessToken,
			"Authorization value": authorizationValue,
			"Cookie value":        cookieValue,
			"request body":        requestBodyFixture,
			"upstream body":       upstreamFixture,
			"Authorization name":  "Authorization",
			"Cookie name":         "Cookie",
		} {
			if bytes.Contains(artifact, []byte(secret)) {
				t.Errorf("%s leaked %s %q", artifactName, label, printableFixture(secret))
			}
		}
	}
}

func printableFixture(value string) string {
	if len(value) <= 16 {
		return value
	}
	return fmt.Sprintf("%s...%s", value[:8], value[len(value)-4:])
}
