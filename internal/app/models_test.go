package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nvidia-router/tests/mocknvidia"
)

func TestAppV1ModelsListsWhitelistAndRequiresAccessKey(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{"data":[{"id":"vendor/model"}]}`})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	// Whitelist lists the seeded model; access key required.
	response, err := http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatalf("get models without key: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-key status = %d, want 401", response.StatusCode)
	}

	authed, err := newAuthedGet(server.URL+"/v1/models", accessToken)
	if err != nil {
		t.Fatalf("authed get: %v", err)
	}
	defer func() { _ = authed.Body.Close() }()
	body, _ := io.ReadAll(authed.Body)
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("authed status = %d, want 200; body=%s", authed.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "public-model") {
		t.Fatalf("whitelist missing public-model; body=%s", string(body))
	}
	if strings.Contains(string(body), "vendor/model") {
		t.Fatalf("upstream id leaked: %s", string(body))
	}
}

func TestAppV1UnknownPathReturnsNotImplementedAndSkipsNVIDIA(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{}`})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	authed, err := newAuthedGet(server.URL+"/v1/some/unknown", accessToken)
	if err != nil {
		t.Fatalf("get unknown: %v", err)
	}
	defer func() { _ = authed.Body.Close() }()
	if authed.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", authed.StatusCode)
	}
	// NVIDIA was not contacted for an unsupported path.
	if upstream.Count() != 0 {
		t.Fatalf("NVIDIA contacted %d times for unsupported path", upstream.Count())
	}
}

func TestAppV1UnknownPathsRequireAccessKeyBeforeUnsupportedResponse(t *testing.T) {
	upstream := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: `{}`})
	t.Cleanup(upstream.Close)
	application, accessToken := newChatTestApp(t, upstream, []string{"upstream-key-1"}, true)
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	for _, path := range []string{"/v1/unknown", "/v1/unknown/nested"} {
		for _, authorization := range []string{"", "Bearer invalid"} {
			request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("request %s: %v", path, err)
			}
			var payload struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				_ = response.Body.Close()
				t.Fatalf("decode %s response: %v", path, err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized || payload.Error.Code != "invalid_api_key" {
				t.Fatalf("%s auth=%q status/code = %d/%q, want 401/invalid_api_key", path, authorization, response.StatusCode, payload.Error.Code)
			}
		}

		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatalf("create authenticated request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+accessToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("authenticated request %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotImplemented {
			t.Fatalf("authenticated %s status = %d, want 501", path, response.StatusCode)
		}
	}
}

func newAuthedGet(target, accessToken string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	return http.DefaultClient.Do(request)
}
