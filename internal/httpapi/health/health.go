package health

import (
	"context"
	"database/sql"
	"net/http"

	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
)

type Handler struct {
	db       *sql.DB
	keys     *crypto.KeySet
	shutting func() bool
}

func New(db *sql.DB, keys *crypto.KeySet, shutting func() bool) *Handler {
	return &Handler{db: db, keys: keys, shutting: shutting}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch request.URL.Path {
	case "/health/live":
		writeStatus(writer, http.StatusOK, "ok")
	case "/health/ready":
		if h.ready(request.Context()) {
			writeStatus(writer, http.StatusOK, "ok")
			return
		}
		writeStatus(writer, http.StatusServiceUnavailable, "unavailable")
	default:
		http.NotFound(writer, request)
	}
}

func (h *Handler) ready(ctx context.Context) bool {
	if h.shutting == nil || h.shutting() || h.db == nil || h.db.PingContext(ctx) != nil {
		return false
	}
	if database.VerifyMigrations(ctx, h.db) != nil {
		return false
	}
	if h.keys == nil || h.keys.ValidateSentinel(ctx, h.db) != nil {
		return false
	}
	var mustChange int
	if err := h.db.QueryRowContext(ctx, "SELECT must_change_password FROM admins WHERE id = 1").Scan(&mustChange); err != nil {
		return false
	}
	return mustChange == 0
}

func writeStatus(writer http.ResponseWriter, status int, value string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(`{"status":"` + value + `"}`))
}
