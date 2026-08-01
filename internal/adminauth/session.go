package adminauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/crypto"
)

const (
	sessionTokenBytes = 32
	sessionIDBytes    = 16
	sessionLifetime   = 24 * time.Hour
	sessionCookieName = "nvr_admin_session"
)

var ErrInvalidSession = errors.New("invalid admin session")

type SessionService struct {
	db     *sql.DB
	clock  clock.Clock
	keys   *crypto.KeySet
	secure bool
}

type CreatedSession struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

type Session struct {
	ID        string
	ExpiresAt time.Time
}

func NewSessionService(db *sql.DB, source clock.Clock, keys *crypto.KeySet, secure bool) *SessionService {
	if source == nil {
		source = clock.RealClock{}
	}
	return &SessionService{db: db, clock: source, keys: keys, secure: secure}
}

// SessionCookie returns a new session cookie for secure=false deployments.
// Use SecureSessionCookie when the admin UI is served over HTTPS.
func SessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   false,
	}
}

// SecureSessionCookie returns a new session cookie with Secure=true
// for deployments where the admin UI is served over HTTPS.
func SecureSessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
	}
}

// ClearSessionCookie returns a session-clearing cookie for secure=false deployments.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   false,
	}
}

// ClearSecureSessionCookie returns a session-clearing cookie with Secure=true.
func ClearSecureSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
	}
}

// MakeSessionCookie returns the appropriate session cookie based on the
// service's secure flag.
func (s *SessionService) MakeSessionCookie(token string) *http.Cookie {
	if s.secure {
		return SecureSessionCookie(token)
	}
	return SessionCookie(token)
}

// MakeClearSessionCookie returns the appropriate clearing cookie based on the
// service's secure flag.
func (s *SessionService) MakeClearSessionCookie() *http.Cookie {
	if s.secure {
		return ClearSecureSessionCookie()
	}
	return ClearSessionCookie()
}

func (s *SessionService) Create(ctx context.Context) (CreatedSession, error) {
	token, err := randomRawURL(sessionTokenBytes)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate admin session token: %w", err)
	}
	id, err := randomRawURL(sessionIDBytes)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate admin session ID: %w", err)
	}

	now := s.clock.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(sessionLifetime)
	tokenBytes := []byte(token)
	digest := s.keys.SessionDigest(tokenBytes)
	crypto.Zero(tokenBytes)
	defer crypto.Zero(digest)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_sessions (id, token_digest, expires_at, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, digest, timestamp(expiresAt), timestamp(now), timestamp(now)); err != nil {
		return CreatedSession{}, fmt.Errorf("insert admin session: %w", err)
	}
	return CreatedSession{ID: id, Token: token, ExpiresAt: expiresAt}, nil
}

func (s *SessionService) Authenticate(ctx context.Context, token string) (Session, error) {
	if !validSessionToken(token) {
		return Session{}, ErrInvalidSession
	}
	tokenBytes := []byte(token)
	digest := s.keys.SessionDigest(tokenBytes)
	crypto.Zero(tokenBytes)
	defer crypto.Zero(digest)

	var session Session
	var expiresAt string
	now := s.clock.Now().UTC()
	err := s.db.QueryRowContext(ctx, `
		UPDATE admin_sessions
		SET last_seen_at = ?
		WHERE token_digest = ? AND revoked_at IS NULL AND expires_at > ?
		RETURNING id, expires_at
	`, timestamp(now), digest, timestamp(now)).Scan(&session.ID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("load admin session: %w", err)
	}
	parsedExpiry, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse admin session expiry: %w", err)
	}
	session.ExpiresAt = parsedExpiry
	return session, nil
}

func (s *SessionService) Revoke(ctx context.Context, sessionID string) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE admin_sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", timestamp(s.clock.Now()), sessionID); err != nil {
		return fmt.Errorf("revoke admin session: %w", err)
	}
	return nil
}

func (s *SessionService) RevokeAll(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE admin_sessions SET revoked_at = ? WHERE revoked_at IS NULL", timestamp(s.clock.Now())); err != nil {
		return fmt.Errorf("revoke all admin sessions: %w", err)
	}
	return nil
}

func randomRawURL(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	defer crypto.Zero(value)
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validSessionToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(sessionTokenBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(decoded) != sessionTokenBytes {
		crypto.Zero(decoded)
		return false
	}
	crypto.Zero(decoded)
	return true
}
