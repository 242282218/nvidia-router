package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"nvidia-router/internal/app"
	"nvidia-router/internal/config"
)

const (
	validModel        = "meta/llama-3.1-8b-instruct"
	validFixtureKey   = "fixture-second-valid-key-123456789"
	allowedFixtureKey = "nvapi-fixture-not-a-real-key-123456789"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() (runErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tempDir, err := os.MkdirTemp("", "nvidia-router-e2e-")
	if err != nil {
		return err
	}
	dataDir := filepath.Join(tempDir, "data")
	workDir := filepath.Join(tempDir, "tmp")
	defer func() {
		cleanupFiles(dataDir, workDir)
		if err := os.RemoveAll(tempDir); runErr == nil && err != nil {
			runErr = fmt.Errorf("remove e2e temp dir: %w", err)
		}
	}()
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		return err
	}

	upstream := httptest.NewServer(http.HandlerFunc(mockNVIDIA))
	defer upstream.Close()
	baseURL, err := url.Parse(upstream.URL)
	if err != nil {
		return err
	}
	address, err := freeAddress()
	if err != nil {
		return err
	}
	var masterKey [32]byte
	if _, err := rand.Read(masterKey[:]); err != nil {
		return err
	}

	encodedMasterKey := base64.RawURLEncoding.EncodeToString(masterKey[:])
	for name, value := range map[string]string{
		"NVIDIA_ROUTER_LISTEN_ADDR":            address,
		"NVIDIA_ROUTER_DATA_DIR":               dataDir,
		"NVIDIA_ROUTER_TEMP_DIR":               workDir,
		"NVIDIA_ROUTER_MASTER_KEY":             encodedMasterKey,
		"NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD": "e2e-initial-admin-password",
		"NVIDIA_ROUTER_NVIDIA_BASE_URL":        baseURL.String(),
	} {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}
	loadedConfig, err := config.LoadFromEnv(config.LoadOptions{AllowInsecureTestUpstream: true})
	if err != nil {
		return fmt.Errorf("load e2e config: %w", err)
	}
	application, err := app.New(ctx, app.Dependencies{
		Config:           loadedConfig,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		NVIDIAHTTPClient: upstream.Client(),
	})
	if err != nil {
		return err
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- application.Serve(ctx) }()
	fmt.Printf("http://%s\n", address)
	if err := <-serveErr; err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func freeAddress() (string, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			lastErr = err
			continue
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			return "", fmt.Errorf("release local port: %w", err)
		}
		return address, nil
	}
	return "", fmt.Errorf("reserve local port after 5 attempts: %w", lastErr)
}

func mockNVIDIA(writer http.ResponseWriter, request *http.Request) {
	token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if request.URL.Path == "/v1/models" && request.Method == http.MethodGet {
		if invalidCredential(token) {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "invalid credential"}})
			return
		}
		writer.Header().Set("X-Request-Id", "e2e-models-request")
		writeJSON(writer, http.StatusOK, map[string]any{"data": []map[string]string{
			{"id": validModel},
			{"id": "nvidia/embedding-qa-4"},
		}})
		return
	}
	if request.URL.Path == "/v1/chat/completions" && request.Method == http.MethodPost {
		if invalidCredential(token) {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "invalid credential"}})
			return
		}
		writer.Header().Set("X-Request-Id", "e2e-chat-request")
		writeJSON(writer, http.StatusOK, map[string]any{
			"id":      "chatcmpl-e2e",
			"object":  "chat.completion",
			"model":   validModel,
			"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
		})
		return
	}
	if request.URL.Path == "/v1/validate" && request.Method == http.MethodPost {
		if invalidCredential(token) {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"valid": false, "error": "invalid credential"})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"valid": true, "models": []string{validModel}})
		return
	}
	http.NotFound(writer, request)
}

func invalidCredential(token string) bool {
	return token != validFixtureKey && token != allowedFixtureKey
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func cleanupFiles(dataDir, workDir string) {
	for _, path := range []string{
		filepath.Join(dataDir, "router.db"),
		filepath.Join(dataDir, "router.db-wal"),
		filepath.Join(dataDir, "router.db-shm"),
	} {
		_ = os.Remove(path)
	}
	_ = os.RemoveAll(workDir)
}
