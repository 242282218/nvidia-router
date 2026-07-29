package adminauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"

	"nvidia-router/internal/crypto"
)

func TestSessionCreateStoresOnlyDigestAndAuthenticates(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	keys := newSessionTestKeySet(t)
	service := NewSessionService(db, fixedClock{now: now}, keys)

	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(created.Token) != base64.RawURLEncoding.EncodedLen(32) {
		t.Fatalf("session token length = %d, want 43", len(created.Token))
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(created.Token); err != nil {
		t.Fatalf("session token is not RawURL base64: %v", err)
	}
	if len(created.ID) != base64.RawURLEncoding.EncodedLen(16) {
		t.Fatalf("session ID length = %d, want 22", len(created.ID))
	}
	if created.ExpiresAt != now.Add(24*time.Hour) {
		t.Fatalf("session expiry = %s, want %s", created.ExpiresAt, now.Add(24*time.Hour))
	}

	var storedDigest []byte
	var storedExpiry string
	if err := db.QueryRow("SELECT token_digest, expires_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&storedDigest, &storedExpiry); err != nil {
		t.Fatalf("read created session: %v", err)
	}
	if bytes.Equal(storedDigest, []byte(created.Token)) {
		t.Fatal("database stored the session token instead of its digest")
	}
	if want := keys.SessionDigest([]byte(created.Token)); !bytes.Equal(storedDigest, want) {
		t.Fatal("database session digest does not match SessionDigest")
	}
	if storedExpiry != created.ExpiresAt.Format(time.RFC3339) {
		t.Fatalf("stored expiry = %q, want %q", storedExpiry, created.ExpiresAt.Format(time.RFC3339))
	}

	authenticated, err := service.Authenticate(context.Background(), created.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authenticated.ID != created.ID || !authenticated.ExpiresAt.Equal(created.ExpiresAt) {
		t.Fatal("authenticated session does not match the created session")
	}
}

func TestSessionAuthenticationRejectsRevokedAndExpiredSessions(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	service := NewSessionService(db, fixedClock{now: now}, newSessionTestKeySet(t))

	revoked, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create revoked session: %v", err)
	}
	if err := service.Revoke(context.Background(), revoked.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	assertInvalidSession(t, service, revoked.Token)

	expired, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create expired session: %v", err)
	}
	if _, err := db.Exec("UPDATE admin_sessions SET expires_at = ? WHERE id = ?", now.Add(-time.Second).Format(time.RFC3339), expired.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	assertInvalidSession(t, service, expired.Token)
}

func TestSessionRevokeAllInvalidatesAllSessions(t *testing.T) {
	db := newTestDatabase(t)
	service := NewSessionService(db, fixedClock{now: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)}, newSessionTestKeySet(t))
	first, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create first session: %v", err)
	}
	second, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create second session: %v", err)
	}
	if err := service.RevokeAll(context.Background()); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	assertInvalidSession(t, service, first.Token)
	assertInvalidSession(t, service, second.Token)
}

func TestSessionCookieHasRequiredAttributes(t *testing.T) {
	cookie := SessionCookie("session-token")
	if cookie.Name != "nvr_admin_session" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 86400 {
		t.Fatal("session cookie does not have the required security attributes")
	}
	if cookie.Secure {
		t.Fatal("session cookie Secure = true, want false for the initial HTTP-only deployment")
	}

	cleared := ClearSessionCookie()
	if cleared.Name != cookie.Name || !cleared.HttpOnly || cleared.SameSite != http.SameSiteStrictMode || cleared.Path != "/" || cleared.MaxAge >= 0 {
		t.Fatal("cleared session cookie does not preserve secure deletion attributes")
	}
}

func assertInvalidSession(t *testing.T, service *SessionService, token string) {
	t.Helper()
	_, err := service.Authenticate(context.Background(), token)
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Authenticate error does not match ErrInvalidSession: %v", err)
	}
	if err != nil && bytes.Contains([]byte(err.Error()), []byte(token)) {
		t.Fatal("Authenticate error leaked the session token")
	}
}

func newSessionTestKeySet(t *testing.T) *crypto.KeySet {
	t.Helper()
	var master [32]byte
	master[0] = 1
	keys, err := crypto.New(master)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return keys
}
