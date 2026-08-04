package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/config"
)

func TestNVIDIAKeyAndModelManagementApplyPoolStateImmediately(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"data":[{"id":"vendor/chat"}]}`)
		case "/v1/chat/completions":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)
	baseURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: t.TempDir(), TempDir: t.TempDir(), MasterKey: [32]byte{1}, NVIDIABaseURL: baseURL},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if _, err := app.db.Exec(`UPDATE admins SET must_change_password = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	login := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/auth/login", `{"username":"admin","password":"test-initial-admin-password"}`, nil, server.URL)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login: %s", readResponse(t, login))
	}
	session := responseSessionCookie(t, login)
	_ = login.Body.Close()

	secret := "nvapi-management-secret"
	created := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/nvidia-keys", `{"key":"`+secret+`"}`, session, server.URL)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", created.StatusCode, readResponse(t, created))
	}
	var imported struct {
		Key struct {
			ID int64 `json:"id"`
		} `json:"key"`
	}
	if err := json.NewDecoder(created.Body).Decode(&imported); err != nil {
		t.Fatal(err)
	}
	_ = created.Body.Close()
	if imported.Key.ID == 0 {
		t.Fatal("missing imported key id")
	}

	listed := authRequest(t, server.Client(), http.MethodGet, server.URL+"/admin/api/nvidia-keys", "", session, "")
	payload := readResponse(t, listed)
	for _, forbidden := range []string{secret, "ciphertext", "nonce", "fingerprint", "digest"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("list leaked %q: %s", forbidden, payload)
		}
	}
	lease, err := app.Pool.Acquire(context.Background(), 0, nil)
	if err != nil {
		t.Fatalf("acquire imported key: %v", err)
	}
	lease.Release()

	patched := authRequest(t, server.Client(), http.MethodPatch, server.URL+"/admin/api/nvidia-keys/"+itoa(imported.Key.ID), `{"enabled":false}`, session, server.URL)
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", patched.StatusCode, readResponse(t, patched))
	}
	_ = patched.Body.Close()
	assertPoolAcquireFails(t, app, 0)
	patched = authRequest(t, server.Client(), http.MethodPatch, server.URL+"/admin/api/nvidia-keys/"+itoa(imported.Key.ID), `{"enabled":true}`, session, server.URL)
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", patched.StatusCode, readResponse(t, patched))
	}
	_ = patched.Body.Close()

	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	result, err := app.db.Exec(`INSERT INTO models(public_id,upstream_id,display_name,kind,enabled,reasoning_wire_format,created_at,updated_at) VALUES(?,?,?,?,1,'none',?,?)`, "chat", "vendor/chat", "Chat", "chat", now, now)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := result.LastInsertId()
	if _, err := app.db.Exec(`INSERT INTO nvidia_key_model_blocks(nvidia_key_id,model_id,reason_code,first_seen_at,last_seen_at) VALUES(?,?,?, ?, ?)`, imported.Key.ID, modelID, "model_not_available", now, now); err != nil {
		t.Fatal(err)
	}
	app.Pool.SetModelBlock(imported.Key.ID, modelID, true)
	assertPoolAcquireFails(t, app, modelID)
	crossOrigin := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/models/"+itoa(modelID)+"/test", `{"key_id":`+itoa(imported.Key.ID)+`}`, session, "https://attacker.example")
	if crossOrigin.StatusCode != http.StatusForbidden || !strings.Contains(readResponse(t, crossOrigin), "invalid_origin") {
		t.Fatalf("cross-origin verification status=%d", crossOrigin.StatusCode)
	}
	unauthenticated := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/models/"+itoa(modelID)+"/test", `{"key_id":`+itoa(imported.Key.ID)+`}`, nil, server.URL)
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated verification status=%d", unauthenticated.StatusCode)
	}
	assertPoolAcquireFails(t, app, modelID)
	unblocked := authRequest(t, server.Client(), http.MethodDelete, server.URL+"/admin/api/key-model-blocks/"+itoa(imported.Key.ID)+"/"+itoa(modelID), "", session, server.URL)
	if unblocked.StatusCode != http.StatusOK {
		t.Fatalf("unblock status=%d body=%s", unblocked.StatusCode, readResponse(t, unblocked))
	}
	_ = unblocked.Body.Close()
	lease, err = app.Pool.Acquire(context.Background(), modelID, nil)
	if err != nil {
		t.Fatalf("acquire after unblock: %v", err)
	}
	lease.Release()

	deleted := authRequest(t, server.Client(), http.MethodDelete, server.URL+"/admin/api/nvidia-keys/"+itoa(imported.Key.ID), "", session, server.URL)
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.StatusCode, readResponse(t, deleted))
	}
	_ = deleted.Body.Close()
	assertPoolAcquireFails(t, app, modelID)
}

func assertPoolAcquireFails(t *testing.T, app *App, modelID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if lease, err := app.Pool.Acquire(ctx, modelID, nil); err == nil {
		lease.Release()
		t.Fatalf("Acquire(%d) succeeded", modelID)
	}
}
func itoa(value int64) string { return strconv.FormatInt(value, 10) }
