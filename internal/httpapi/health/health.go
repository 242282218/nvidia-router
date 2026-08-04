package health

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
)

// readyProbeTimeout bounds every /health/ready probe. The writer runs on a
// single shared connection with a 5s busy timeout, so without a probe deadline
// a connection wedged behind a long write would make each probe wait up to 5s,
// letting orchestrator probes pile up instead of failing fast.
const readyProbeTimeout = 2 * time.Second

type Handler struct {
	db       *sql.DB
	reader   *sql.DB
	keys     *crypto.KeySet
	shutting func() bool
}

func New(db *sql.DB, keys *crypto.KeySet, shutting func() bool) *Handler {
	return &Handler{db: db, keys: keys, shutting: shutting}
}

// WithReader routes the probe's query-based checks to the read-only pool. Both
// pools are still probed, because serving requires both, but only the writer
// liveness check has to contend with in-flight writes.
func (h *Handler) WithReader(reader *sql.DB) *Handler {
	clone := *h
	clone.reader = reader
	return &clone
}

func (h *Handler) read() *sql.DB {
	if h.reader != nil {
		return h.reader
	}
	return h.db
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
	if h.shutting == nil || h.shutting() || h.db == nil {
		return false
	}
	// Fail the probe quickly instead of queueing behind a busy business
	// request on the single shared connection (see readyProbeTimeout).
	probe, cancel := context.WithTimeout(ctx, readyProbeTimeout)
	defer cancel()
	if h.db.PingContext(probe) != nil {
		return false
	}
	read := h.read()
	if read != h.db && read.PingContext(probe) != nil {
		return false
	}
	if database.VerifyMigrations(probe, read) != nil {
		return false
	}
	// Deliberately do NOT gate readiness on must_change_password: that flag
	// describes an administrator-driven security posture (log in then change
	// the bootstrap password), not service health. An orchestrator probing
	// /health/ready right after first boot would otherwise report the
	// container as unhealthy until someone logs in, restarting it in a
	// loop and locking out the very first sign-in the policy is waiting for.
	if h.keys == nil || h.keys.ValidateSentinel(probe, read) != nil {
		return false
	}
	return true
}

func writeStatus(writer http.ResponseWriter, status int, value string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(`{"status":"` + value + `"}`))
}
