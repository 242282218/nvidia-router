package accesskey

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMiddlewareRequiresExactBearerAndReturnsOpenAIError(t *testing.T) {
	service, _ := newTestService(t, filepath.Join(t.TempDir(), "router.db"))
	for _, authorization := range []string{"", "Basic abc", "Bearer", "Bearer  invalid", "bearer invalid", "Bearer invalid"} {
		t.Run(authorization, func(t *testing.T) {
			called := false
			handler := Middleware(service, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if called || response.Code != http.StatusUnauthorized {
				t.Fatalf("called/status = %v/%d", called, response.Code)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if body.Error.Code != "invalid_api_key" {
				t.Fatalf("error code = %q", body.Error.Code)
			}
		})
	}
}

func TestMiddlewareStoresOnlyIdentityAndCoalescesLastUsed(t *testing.T) {
	source := newManualClock(time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC))
	service, db := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)
	created, err := service.Create(context.Background(), "sdk")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got AccessKeyIdentity
	handler := Middleware(service, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var ok bool
		got, ok = IdentityFromContext(request.Context())
		if !ok {
			t.Fatal("identity missing from context")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+created.Plaintext)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || got.ID != created.Key.ID || got.Prefix != created.Key.Prefix {
		t.Fatalf("status/identity = %d/%+v", response.Code, got)
	}
	if strings.Contains(strings.Join([]string{got.Prefix}, ""), created.Plaintext) {
		t.Fatal("context identity contains plaintext")
	}
	waitLastUsed(t, db, created.Key.ID, source.Now())
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock(now time.Time) *manualClock { return &manualClock{now: now} }

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func (*manualClock) NewTimer(duration time.Duration) *time.Timer { return time.NewTimer(duration) }
func (*manualClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}
