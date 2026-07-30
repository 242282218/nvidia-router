package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/apierror"
)

const (
	sessionCookieName = "nvr_admin_session"
	maxAuthBodyBytes  = 64 << 10
)

type Auth struct {
	repository *adminauth.Repository
	sessions   *adminauth.SessionService
	limiter    *adminauth.LoginLimiter
}

type sessionResponse struct {
	Authenticated      bool `json:"authenticated"`
	MustChangePassword bool `json:"must_change_password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func NewAuth(repository *adminauth.Repository, sessions *adminauth.SessionService, limiter *adminauth.LoginLimiter) *Auth {
	return &Auth{repository: repository, sessions: sessions, limiter: limiter}
}

func (a *Auth) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if err := adminauth.ValidateOrigin(request); err != nil {
		writeInvalidOrigin(writer)
		return
	}
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/admin/api/auth/login":
		a.login(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/admin/api/auth/session":
		a.session(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/admin/api/auth/change-password":
		a.changePassword(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/admin/api/auth/logout":
		a.logout(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/admin/api/auth/revoke-all":
		a.revokeAll(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (a *Auth) RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mustChange, err := a.repository.MustChangePassword(request.Context())
		if err != nil {
			writeInternalError(writer, err)
			return
		}
		if mustChange {
			writePasswordChangeRequired(writer)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (a *Auth) RequireManagement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := adminauth.ValidateOrigin(request); err != nil {
			writeInvalidOrigin(writer)
			return
		}
		if _, ok := a.requireSession(writer, request, true); !ok {
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (a *Auth) login(writer http.ResponseWriter, request *http.Request) {
	ip := clientIP(request.RemoteAddr)
	if err := a.limiter.StartAttempt(ip); err != nil {
		writeRateLimit(writer)
		return
	}

	var input loginRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		a.recordLoginFailure(request.Context(), ip)
		writeInvalidRequest(writer, "The login request is invalid.", err)
		return
	}
	matched, err := a.repository.VerifyCredentials(request.Context(), input.Username, input.Password)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if !matched {
		a.recordLoginFailure(request.Context(), ip)
		writeInvalidCredentials(writer)
		return
	}

	created, err := a.sessions.Create(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	mustChange, err := a.repository.MustChangePassword(request.Context())
	if err != nil {
		_ = a.sessions.Revoke(request.Context(), created.ID)
		writeInternalError(writer, err)
		return
	}
	a.limiter.RecordSuccess(ip)
	http.SetCookie(writer, adminauth.SessionCookie(created.Token))
	writeSession(writer, mustChange)
}

func (a *Auth) session(writer http.ResponseWriter, request *http.Request) {
	if _, ok := a.requireSession(writer, request, false); !ok {
		return
	}
	mustChange, err := a.repository.MustChangePassword(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeSession(writer, mustChange)
}

func (a *Auth) changePassword(writer http.ResponseWriter, request *http.Request) {
	_, ok := a.requireSession(writer, request, false)
	if !ok {
		return
	}
	var input changePasswordRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The password change request is invalid.", err)
		return
	}

	replacement, err := a.sessions.Create(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if err := a.repository.ChangePassword(request.Context(), input.CurrentPassword, input.NewPassword, replacement.ID); err != nil {
		cleanupErr := a.sessions.Revoke(request.Context(), replacement.ID)
		writePasswordChangeError(writer, errors.Join(err, cleanupErr))
		return
	}
	http.SetCookie(writer, adminauth.SessionCookie(replacement.Token))
	writeSession(writer, false)

}

func (a *Auth) logout(writer http.ResponseWriter, request *http.Request) {
	current, ok := a.requireSession(writer, request, false)
	if !ok {
		return
	}
	if err := a.sessions.Revoke(request.Context(), current.ID); err != nil {
		writeInternalError(writer, err)
		return
	}
	http.SetCookie(writer, adminauth.ClearSessionCookie())
	writer.WriteHeader(http.StatusNoContent)
}

func (a *Auth) revokeAll(writer http.ResponseWriter, request *http.Request) {
	if _, ok := a.requireSession(writer, request, true); !ok {
		return
	}
	if err := a.sessions.RevokeAll(request.Context()); err != nil {
		writeInternalError(writer, err)
		return
	}
	http.SetCookie(writer, adminauth.ClearSessionCookie())
	writer.WriteHeader(http.StatusNoContent)
}

func (a *Auth) requireSession(writer http.ResponseWriter, request *http.Request, requireChanged bool) (adminauth.Session, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeInvalidSession(writer)
		return adminauth.Session{}, false
	}
	session, err := a.sessions.Authenticate(request.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, adminauth.ErrInvalidSession) {
			writeInvalidSession(writer)
		} else {
			writeInternalError(writer, err)
		}
		return adminauth.Session{}, false
	}
	if !requireChanged {
		return session, true
	}
	mustChange, err := a.repository.MustChangePassword(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return adminauth.Session{}, false
	}
	if mustChange {
		writePasswordChangeRequired(writer)
		return adminauth.Session{}, false
	}
	return session, true
}

func (a *Auth) recordLoginFailure(ctx context.Context, ip string) {
	_ = a.limiter.RecordFailure(ctx, ip)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxAuthBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode JSON body: multiple values")
		}
		return fmt.Errorf("finish JSON body: %w", err)
	}
	return nil
}

func clientIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddress)
}

func writeSession(writer http.ResponseWriter, mustChange bool) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(sessionResponse{Authenticated: true, MustChangePassword: mustChange})
}

func writeInvalidRequest(writer http.ResponseWriter, message string, cause error) {
	apierror.Error{Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "invalid_request", Message: message, Cause: cause}.Write(writer)
}

func writeInvalidOrigin(writer http.ResponseWriter) {
	apierror.Error{Status: http.StatusForbidden, Type: "authentication_error", Code: "invalid_origin", Message: "The request origin is not allowed."}.Write(writer)
}

func writeInvalidCredentials(writer http.ResponseWriter) {
	apierror.Error{Status: http.StatusUnauthorized, Type: "authentication_error", Code: "invalid_credentials", Message: "The username or password is incorrect."}.Write(writer)
}

func writeInvalidSession(writer http.ResponseWriter) {
	apierror.Error{Status: http.StatusUnauthorized, Type: "authentication_error", Code: "invalid_session", Message: "The administrator session is invalid or expired."}.Write(writer)
}

func writePasswordChangeRequired(writer http.ResponseWriter) {
	apierror.Error{Status: http.StatusForbidden, Type: "authentication_error", Code: "password_change_required", Message: "The default administrator password must be changed first."}.Write(writer)
}

func writeRateLimit(writer http.ResponseWriter) {
	apierror.Error{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "rate_limit_exceeded", Message: "Too many login attempts. Please retry later.", RetryAfter: time.Minute}.Write(writer)
}

func writePasswordChangeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, adminauth.ErrCurrentPasswordIncorrect):
		writeInvalidCredentials(writer)
	case errors.Is(err, adminauth.ErrPasswordTooShort), errors.Is(err, adminauth.ErrPasswordIsDefault):
		writeInvalidRequest(writer, "The new password does not meet the password policy.", err)
	default:
		writeInternalError(writer, err)
	}
}

func writeInternalError(writer http.ResponseWriter, err error) {
	apierror.Error{Status: http.StatusInternalServerError, Type: "server_error", Code: "internal_error", Message: "The server could not complete the request.", Cause: err}.Write(writer)
}
