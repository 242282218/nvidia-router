package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
)

func TestAuthFlowEnforcesGlobalPasswordChangeGate(t *testing.T) {
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{DataDir: t.TempDir(), TempDir: t.TempDir(), MasterKey: [32]byte{1}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		server.Close()
		_ = app.Close()
	})

	login := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, server.URL)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200: %s", login.StatusCode, readResponse(t, login))
	}
	restricted := responseSessionCookie(t, login)
	assertResponseSession(t, login, true)

	dataBlocked := authRequest(t, server.Client(), http.MethodGet, server.URL+"/v1/models", "", nil, "")
	assertResponseError(t, dataBlocked, http.StatusForbidden, "password_change_required")
	managementBlocked := authRequest(t, server.Client(), http.MethodGet, server.URL+"/admin/api/settings", "", restricted, "")
	assertResponseError(t, managementBlocked, http.StatusForbidden, "password_change_required")

	changed := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/auth/change-password", `{"current_password":"admin","new_password":"replacement-password"}`, restricted, server.URL)
	if changed.StatusCode != http.StatusOK {
		t.Fatalf("change password status = %d, want 200: %s", changed.StatusCode, readResponse(t, changed))
	}
	active := responseSessionCookie(t, changed)
	assertResponseSession(t, changed, false)

	oldSession := authRequest(t, server.Client(), http.MethodGet, server.URL+"/admin/api/auth/session", "", restricted, "")
	assertResponseError(t, oldSession, http.StatusUnauthorized, "invalid_session")
	dataAllowed := authRequest(t, server.Client(), http.MethodGet, server.URL+"/v1/models", "", nil, "")
	assertResponseError(t, dataAllowed, http.StatusUnauthorized, "invalid_api_key")
	managementAllowed := authRequest(t, server.Client(), http.MethodGet, server.URL+"/admin/api/settings", "", active, "")
	if managementAllowed.StatusCode != http.StatusNotFound {
		t.Fatalf("management status = %d, want 404: %s", managementAllowed.StatusCode, readResponse(t, managementAllowed))
	}
	_ = managementAllowed.Body.Close()
	crossOrigin := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/settings", "", active, "http://attacker.example")
	assertResponseError(t, crossOrigin, http.StatusForbidden, "invalid_origin")
}

func authRequest(t *testing.T, client *http.Client, method, url, body string, cookie *http.Cookie, origin string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func responseSessionCookie(t *testing.T, response *http.Response) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "nvr_admin_session" && cookie.Value != "" && cookie.MaxAge > 0 {
			return cookie
		}
	}
	t.Fatalf("response has no session cookie: status=%d headers=%v", response.StatusCode, response.Header)
	return nil
}

func assertResponseSession(t *testing.T, response *http.Response, mustChange bool) {
	t.Helper()
	defer response.Body.Close()
	var payload struct {
		Authenticated      bool `json:"authenticated"`
		MustChangePassword bool `json:"must_change_password"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if !payload.Authenticated || payload.MustChangePassword != mustChange {
		t.Fatalf("session response = %+v, want authenticated=true must_change_password=%t", payload, mustChange)
	}
}

func assertResponseError(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, status, body)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Code != code {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, code)
	}
}

func readResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return string(body)
}
