package adminauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/crypto"
)

func TestSessionCreateStoresOnlyDigestAndAuthenticates(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	keys := newSessionTestKeySet(t)
	service := NewSessionService(db, fixedClock{now: now}, keys, false)

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

func TestSessionCreateReturnsPersistedFullLifetimeAtUTCSecondPrecision(t *testing.T) {
	db := newTestDatabase(t)
	localZone := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.July, 29, 20, 0, 0, 987654321, localZone)
	service := NewSessionService(db, fixedClock{now: now}, newSessionTestKeySet(t), false)

	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantExpiry := now.UTC().Truncate(time.Second).Add(24 * time.Hour)
	if created.ExpiresAt != wantExpiry {
		t.Fatalf("returned expiry = %s, want %s", created.ExpiresAt, wantExpiry)
	}

	var storedExpiry string
	if err := db.QueryRow("SELECT expires_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&storedExpiry); err != nil {
		t.Fatalf("read stored expiry: %v", err)
	}
	parsedExpiry, err := time.Parse(time.RFC3339, storedExpiry)
	if err != nil {
		t.Fatalf("parse stored expiry: %v", err)
	}
	if parsedExpiry != created.ExpiresAt {
		t.Fatalf("stored expiry = %s, returned expiry = %s", parsedExpiry, created.ExpiresAt)
	}
}

func TestSessionAuthenticateUpdatesLastSeenWithoutExtendingExpiry(t *testing.T) {
	db := newTestDatabase(t)
	testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
	service := NewSessionService(db, testClock, newSessionTestKeySet(t), false)
	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var originalExpiry string
	if err := db.QueryRow("SELECT expires_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&originalExpiry); err != nil {
		t.Fatalf("read original expiry: %v", err)
	}
	testClock.Advance(2 * time.Hour)
	authenticated, err := service.Authenticate(context.Background(), created.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	var storedExpiry, lastSeen string
	if err := db.QueryRow("SELECT expires_at, last_seen_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&storedExpiry, &lastSeen); err != nil {
		t.Fatalf("read authenticated session: %v", err)
	}
	if storedExpiry != originalExpiry || authenticated.ExpiresAt != created.ExpiresAt {
		t.Fatal("Authenticate extended the fixed session expiry")
	}
	if want := timestamp(testClock.Now()); lastSeen != want {
		t.Fatalf("last_seen_at = %q, want %q", lastSeen, want)
	}
}

func TestSessionAuthenticateThrottlesLastSeenWrites(t *testing.T) {
	db := newTestDatabase(t)
	testClock := newLimiterClock(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC))
	service := NewSessionService(db, testClock, newSessionTestKeySet(t), false)
	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var initial string
	if err := db.QueryRow("SELECT last_seen_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&initial); err != nil {
		t.Fatalf("read initial last_seen_at: %v", err)
	}

	testClock.Advance(30 * time.Second)
	if _, err := service.Authenticate(context.Background(), created.Token); err != nil {
		t.Fatalf("Authenticate within window: %v", err)
	}
	var withinWindow string
	if err := db.QueryRow("SELECT last_seen_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&withinWindow); err != nil {
		t.Fatalf("read throttled last_seen_at: %v", err)
	}
	if withinWindow != initial {
		t.Fatalf("last_seen_at changed within throttle window: %q -> %q", initial, withinWindow)
	}

	testClock.Advance(2 * time.Minute)
	if _, err := service.Authenticate(context.Background(), created.Token); err != nil {
		t.Fatalf("Authenticate after window: %v", err)
	}
	var afterWindow string
	if err := db.QueryRow("SELECT last_seen_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&afterWindow); err != nil {
		t.Fatalf("read refreshed last_seen_at: %v", err)
	}
	if afterWindow == initial {
		t.Fatal("last_seen_at did not refresh after the throttle window")
	}
}

func TestSessionAuthenticateRejectsRevocationBeforeFinalTouch(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	clock := newLimiterClock(now)
	service := NewSessionService(db, clock, newSessionTestKeySet(t), false)
	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER revoke_before_session_touch
		BEFORE UPDATE OF last_seen_at ON admin_sessions
		BEGIN
			UPDATE admin_sessions
			SET revoked_at = '2026-07-29T12:00:00Z'
			WHERE id = OLD.id;
			SELECT RAISE(IGNORE);
		END
	`); err != nil {
		t.Fatalf("create revocation trigger: %v", err)
	}
	// The touch is throttled while last_seen_at is fresh, so advance past the
	// throttle window to make the UPDATE (and its trigger) fire.
	clock.Advance(2 * time.Hour)

	_, err = service.Authenticate(context.Background(), created.Token)
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Authenticate during revocation error = %v, want ErrInvalidSession", err)
	}
	var revokedAt string
	if err := db.QueryRow("SELECT revoked_at FROM admin_sessions WHERE id = ?", created.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("read revoked session: %v", err)
	}
	if revokedAt == "" {
		t.Fatal("revocation trigger did not persist revocation")
	}
}

func TestSessionAuthenticationRejectsRevokedAndExpiredSessions(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	service := NewSessionService(db, fixedClock{now: now}, newSessionTestKeySet(t), false)

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
	service := NewSessionService(db, fixedClock{now: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)}, newSessionTestKeySet(t), false)
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

func TestRepositoryDeleteExpiredOrRevokedUsesInclusiveCutoff(t *testing.T) {
	db := newTestDatabase(t)
	repository := NewRepository(db, fixedClock{now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	for _, session := range []struct {
		id        string
		expiresAt string
		revokedAt any
	}{
		{id: "expired-fraction", expiresAt: "2026-07-30T12:00:00.500Z"},
		{id: "expired-second", expiresAt: "2026-07-30T12:00:00Z"},
		{id: "revoked-old", expiresAt: "2026-08-01T00:00:00Z", revokedAt: "2026-07-30T12:00:00.499Z"},
		{id: "revoked-boundary", expiresAt: "2026-08-01T00:00:00Z", revokedAt: "2026-07-30T12:00:00.500Z"},
		{id: "active", expiresAt: "2026-08-01T00:00:00Z"},
	} {
		if _, err := db.Exec(`
			INSERT INTO admin_sessions (id, token_digest, expires_at, created_at, last_seen_at, revoked_at)
			VALUES (?, ?, ?, '2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z', ?)
		`, session.id, []byte(session.id), session.expiresAt, session.revokedAt); err != nil {
			t.Fatalf("insert %s: %v", session.id, err)
		}
	}

	deleted, err := repository.DeleteExpiredOrRevoked(context.Background(), time.Date(2026, 7, 30, 12, 0, 0, 500000000, time.UTC))
	if err != nil {
		t.Fatalf("DeleteExpiredOrRevoked: %v", err)
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d, want 4", deleted)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&remaining); err != nil {
		t.Fatalf("count remaining sessions: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining sessions = %d, want 1", remaining)
	}
}

func TestSessionCleanupWorkerRunsAndStopsOnCancellation(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	repository := &sessionCleanupRepositoryStub{called: make(chan time.Time, 1)}
	waitStarted := make(chan time.Duration, 1)
	ctx, cancel := context.WithCancel(context.Background())
	worker := newSessionCleanupWorker(repository, func() time.Time { return now }, func(ctx context.Context, duration time.Duration) bool {
		waitStarted <- duration
		<-ctx.Done()
		return false
	}, nil)
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	select {
	case cutoff := <-repository.called:
		want := now
		if !cutoff.Equal(want) {
			t.Fatalf("cleanup cutoff = %s, want %s", cutoff, want)
		}
	case <-time.After(time.Second):
		t.Fatal("startup session cleanup was not run")
	}
	select {
	case <-waitStarted:
	case <-time.After(time.Second):
		t.Fatal("session cleanup worker did not schedule next run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session cleanup worker did not stop after cancellation")
	}
}

func TestSessionCleanupWorkerRetriesAfterFailureBeforeDailySchedule(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 30, 0, 0, time.UTC)
	repository := &retryingSessionCleanupRepository{errorsRemaining: 1}
	waits := make([]time.Duration, 0, 2)
	worker := newSessionCleanupWorker(repository, func() time.Time { return now }, func(_ context.Context, duration time.Duration) bool {
		waits = append(waits, duration)
		return len(waits) < 2
	}, nil)

	worker.Run(context.Background())

	if repository.calls != 2 {
		t.Fatalf("cleanup calls = %d, want 2", repository.calls)
	}
	if len(waits) != 2 {
		t.Fatalf("wait calls = %d, want 2", len(waits))
	}
	if waits[0] != time.Minute {
		t.Fatalf("retry wait = %s, want 1m", waits[0])
	}
	// The daily sweep is staggered to 03:30 UTC; from 04:30 that is 23h away.
	if want := 23 * time.Hour; waits[1] != want {
		t.Fatalf("daily wait = %s, want %s", waits[1], want)
	}
}

type retryingSessionCleanupRepository struct {
	errorsRemaining int
	calls           int
}

func (r *retryingSessionCleanupRepository) DeleteExpiredOrRevoked(context.Context, time.Time) (int64, error) {
	r.calls++
	if r.errorsRemaining > 0 {
		r.errorsRemaining--
		return 0, errors.New("temporary repository failure")
	}
	return 0, nil
}

type sessionCleanupRepositoryStub struct {
	called chan time.Time
}

func (s *sessionCleanupRepositoryStub) DeleteExpiredOrRevoked(_ context.Context, cutoff time.Time) (int64, error) {
	s.called <- cutoff
	return 0, nil
}

func TestSessionPasswordChangeKeepsOnlyReplacementSession(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	repository := NewRepository(db, fixedClock{now: now})
	if err := repository.EnsureAdmin(context.Background(), testInitialAdminPassword); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	service := NewSessionService(db, fixedClock{now: now}, newSessionTestKeySet(t), false)
	firstOld, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create first old session: %v", err)
	}
	secondOld, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create second old session: %v", err)
	}
	replacement, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create replacement session: %v", err)
	}

	// Passing an authenticating old session ID would preserve the cookie being rotated out.
	if err := repository.ChangePassword(context.Background(), testInitialAdminPassword, "a replacement password", replacement.ID); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	assertInvalidSession(t, service, firstOld.Token)
	assertInvalidSession(t, service, secondOld.Token)
	if _, err := service.Authenticate(context.Background(), replacement.Token); err != nil {
		t.Fatalf("Authenticate replacement session: %v", err)
	}
}

func TestSessionAuthenticationSeparatesInvalidCredentialsFromDatabaseFailures(t *testing.T) {
	db := newTestDatabase(t)
	service := NewSessionService(db, fixedClock{now: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)}, newSessionTestKeySet(t), false)
	assertInvalidSession(t, service, "not-a-session-token")
	unknownToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, sessionTokenBytes))
	assertInvalidSession(t, service, unknownToken)

	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	_, err = service.Authenticate(context.Background(), created.Token)
	if err == nil || !strings.Contains(err.Error(), "load admin session") {
		t.Fatalf("Authenticate database error lacks operation context: %v", err)
	}
	if errors.Is(err, ErrInvalidSession) {
		t.Fatal("Authenticate classified a database failure as ErrInvalidSession")
	}
}

func TestSecureSessionCookieHasSecureAttribute(t *testing.T) {
	cookie := SecureSessionCookie("session-token")
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 86400 {
		t.Fatalf("secure session cookie = %#v", cookie)
	}
	cleared := ClearSecureSessionCookie()
	if !cleared.Secure || cleared.MaxAge >= 0 {
		t.Fatalf("secure cleared cookie = %#v", cleared)
	}
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
