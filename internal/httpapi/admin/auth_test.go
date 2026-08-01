package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
)

func TestAuthLoginCreatesRestrictedSessionWithRequiredCookie(t *testing.T) {
	fixture := newAuthFixture(t)

	response := fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, sameOrigin)

	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200: %s", response.Code, response.Body.String())
	}
	cookie := requireSessionCookie(t, response)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 86400 || cookie.Secure {
		t.Fatalf("session cookie = %#v", cookie)
	}
	assertSessionResponse(t, response, true)

	session := fixture.request(http.MethodGet, "/admin/api/auth/session", "", cookie, "")
	if session.Code != http.StatusOK {
		t.Fatalf("session status = %d, want 200: %s", session.Code, session.Body.String())
	}
	assertSessionResponse(t, session, true)
}

func TestAuthRejectsInvalidOriginCredentialsAndSixthAttempt(t *testing.T) {
	fixture := newAuthFixture(t)

	crossOrigin := fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, "http://attacker.example")
	assertAPIError(t, crossOrigin, http.StatusForbidden, "invalid_origin")

	for attempt := 1; attempt <= 5; attempt++ {
		response := fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"wrong"}`, nil, sameOrigin)
		assertAPIError(t, response, http.StatusUnauthorized, "invalid_credentials")
	}
	rateLimited := fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"wrong"}`, nil, sameOrigin)
	assertAPIError(t, rateLimited, http.StatusTooManyRequests, "rate_limit_exceeded")
	if rateLimited.Header().Get("Retry-After") == "" {
		t.Fatal("rate limited response is missing Retry-After")
	}
}

func TestAuthChangePasswordRotatesSessionAndLogoutRevokeAll(t *testing.T) {
	fixture := newAuthFixture(t)
	firstOld := requireSessionCookie(t, fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, sameOrigin))
	secondOld := requireSessionCookie(t, fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, sameOrigin))

	changed := fixture.request(http.MethodPost, "/admin/api/auth/change-password", `{"current_password":"admin","new_password":"replacement-password"}`, firstOld, sameOrigin)
	if changed.Code != http.StatusOK {
		t.Fatalf("change password status = %d, want 200: %s", changed.Code, changed.Body.String())
	}
	newCookie := requireSessionCookie(t, changed)
	if newCookie.Value == firstOld.Value {
		t.Fatal("change password reused the old session token")
	}
	assertSessionResponse(t, changed, false)
	assertInvalidSession(t, fixture, firstOld)
	assertInvalidSession(t, fixture, secondOld)

	current := fixture.request(http.MethodGet, "/admin/api/auth/session", "", newCookie, "")
	if current.Code != http.StatusOK {
		t.Fatalf("replacement session status = %d, want 200: %s", current.Code, current.Body.String())
	}
	assertSessionResponse(t, current, false)

	oldPassword := fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, sameOrigin)
	assertAPIError(t, oldPassword, http.StatusUnauthorized, "invalid_credentials")
	firstNew := requireSessionCookie(t, fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"replacement-password"}`, nil, sameOrigin))

	loggedOut := fixture.request(http.MethodPost, "/admin/api/auth/logout", "", firstNew, sameOrigin)
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204: %s", loggedOut.Code, loggedOut.Body.String())
	}
	requireClearedSessionCookie(t, loggedOut)
	assertInvalidSession(t, fixture, firstNew)

	firstActive := requireSessionCookie(t, fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"replacement-password"}`, nil, sameOrigin))
	secondActive := requireSessionCookie(t, fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"replacement-password"}`, nil, sameOrigin))
	revoked := fixture.request(http.MethodPost, "/admin/api/auth/revoke-all", "", firstActive, sameOrigin)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke-all status = %d, want 204: %s", revoked.Code, revoked.Body.String())
	}
	requireClearedSessionCookie(t, revoked)
	assertInvalidSession(t, fixture, firstActive)
	assertInvalidSession(t, fixture, secondActive)
}

func TestAuthGuardsDataAndManagementRoutes(t *testing.T) {
	fixture := newAuthFixture(t)
	restricted := requireSessionCookie(t, fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, sameOrigin))
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })

	dataBlocked := serve(fixture.auth.RequirePasswordChanged(next), http.MethodPost, "/v1/models", "", nil, "")
	assertAPIError(t, dataBlocked, http.StatusForbidden, "password_change_required")
	managementUnauthenticated := serve(fixture.auth.RequireManagement(next), http.MethodGet, "/admin/api/settings", "", nil, "")
	assertAPIError(t, managementUnauthenticated, http.StatusUnauthorized, "invalid_session")
	managementRestricted := serve(fixture.auth.RequireManagement(next), http.MethodGet, "/admin/api/settings", "", restricted, "")
	assertAPIError(t, managementRestricted, http.StatusForbidden, "password_change_required")
	revokeRestricted := fixture.request(http.MethodPost, "/admin/api/auth/revoke-all", "", restricted, sameOrigin)
	assertAPIError(t, revokeRestricted, http.StatusForbidden, "password_change_required")
	logoutRestricted := fixture.request(http.MethodPost, "/admin/api/auth/logout", "", restricted, sameOrigin)
	if logoutRestricted.Code != http.StatusNoContent {
		t.Fatalf("restricted logout status = %d, want 204: %s", logoutRestricted.Code, logoutRestricted.Body.String())
	}
	restricted = requireSessionCookie(t, fixture.request(http.MethodPost, "/admin/api/auth/login", `{"username":"admin","password":"admin"}`, nil, sameOrigin))

	changed := fixture.request(http.MethodPost, "/admin/api/auth/change-password", `{"current_password":"admin","new_password":"replacement-password"}`, restricted, sameOrigin)
	active := requireSessionCookie(t, changed)
	dataAllowed := serve(fixture.auth.RequirePasswordChanged(next), http.MethodPost, "/v1/models", "", nil, "")
	if dataAllowed.Code != http.StatusNoContent {
		t.Fatalf("data guard status = %d, want 204: %s", dataAllowed.Code, dataAllowed.Body.String())
	}
	managementAllowed := serve(fixture.auth.RequireManagement(next), http.MethodGet, "/admin/api/settings", "", active, "")
	if managementAllowed.Code != http.StatusNoContent {
		t.Fatalf("management GET status = %d, want 204: %s", managementAllowed.Code, managementAllowed.Body.String())
	}
	managementCrossOrigin := serve(fixture.auth.RequireManagement(next), http.MethodPost, "/admin/api/settings", "", active, "http://attacker.example")
	assertAPIError(t, managementCrossOrigin, http.StatusForbidden, "invalid_origin")
	managementSameOrigin := serve(fixture.auth.RequireManagement(next), http.MethodPost, "/admin/api/settings", "", active, sameOrigin)
	if managementSameOrigin.Code != http.StatusNoContent {
		t.Fatalf("management POST status = %d, want 204: %s", managementSameOrigin.Code, managementSameOrigin.Body.String())
	}
}

func TestAuthRejectsOversizedBodyWithJSON413(t *testing.T) {
	fixture := newAuthFixture(t)
	body := `{"username":"` + strings.Repeat("x", maxAuthBodyBytes+1) + `","password":"admin"}`
	response := fixture.request(http.MethodPost, "/admin/api/auth/login", body, nil, sameOrigin)

	assertAPIError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

const sameOrigin = "http://example.com"

type authFixture struct {
	auth *Auth
}

func newAuthFixture(t *testing.T) authFixture {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	testClock := instantClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}
	keys, err := crypto.New([32]byte{1})
	if err != nil {
		t.Fatalf("create key set: %v", err)
	}
	if err := keys.EnsureSentinel(context.Background(), db); err != nil {
		t.Fatalf("ensure sentinel: %v", err)
	}
	repository := adminauth.NewRepository(db, testClock)
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	sessions := adminauth.NewSessionService(db, testClock, keys, false)
	return authFixture{auth: NewAuth(repository, sessions, adminauth.NewLoginLimiter(testClock))}
}

func (f authFixture) request(method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	return serve(f.auth, method, path, body, cookie, origin)
}

func serve(handler http.Handler, method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.RemoteAddr = "192.0.2.10:12345"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func requireSessionCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "nvr_admin_session" && cookie.Value != "" && cookie.MaxAge > 0 {
			return cookie
		}
	}
	t.Fatalf("response has no active session cookie: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	return nil
}

func requireClearedSessionCookie(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "nvr_admin_session" && cookie.MaxAge < 0 {
			return
		}
	}
	t.Fatalf("response has no cleared session cookie: headers=%v", response.Header())
}

func assertSessionResponse(t *testing.T, response *httptest.ResponseRecorder, mustChange bool) {
	t.Helper()
	var payload struct {
		Authenticated      bool `json:"authenticated"`
		MustChangePassword bool `json:"must_change_password"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode session response: %v: %s", err, response.Body.String())
	}
	if !payload.Authenticated || payload.MustChangePassword != mustChange {
		t.Fatalf("session response = %+v, want authenticated=true must_change_password=%t", payload, mustChange)
	}
}

func assertInvalidSession(t *testing.T, fixture authFixture, cookie *http.Cookie) {
	t.Helper()
	response := fixture.request(http.MethodGet, "/admin/api/auth/session", "", cookie, "")
	assertAPIError(t, response, http.StatusUnauthorized, "invalid_session")
}

func assertAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode API error: %v: %s", err, response.Body.String())
	}
	if payload.Error.Code != code {
		t.Fatalf("error code = %q, want %q: %s", payload.Error.Code, code, response.Body.String())
	}
}

type instantClock struct {
	clock.RealClock
	now time.Time
}

func (c instantClock) Now() time.Time { return c.now }

func (instantClock) NewTimer(time.Duration) *time.Timer { return time.NewTimer(0) }
