package admin

import (
	"net"
	"net/http"
	"regexp"
	"strings"

	"nvidia-router/internal/adminaudit"
	"nvidia-router/internal/adminauth"
)

// AuditMiddleware appends an admin-audit entry for every mutating request that
// reaches an admin endpoint. It derives the action from the HTTP method +
// normalized path (numeric IDs become ":id"), so any future handler is covered
// automatically without per-handler wiring.
//
// The recorder attributes the entry to the authenticated principal carried in
// the context (injected by RequireManagement). Pre-session requests — login
// attempts — have no principal, so the middleware falls back to the client
// address computed from the request using the same trusted-proxy policy as the
// rest of the admin surface.
func AuditMiddleware(recorder *adminaudit.Recorder, trusted []*net.IPNet, next http.Handler) http.Handler {
	audit := func(writer http.ResponseWriter, request *http.Request, status int) {
		entryCtx := request.Context()
		_, authenticated := adminauth.PrincipalFromContext(request.Context())
		if !authenticated {
			// Attribute failed/early auth attempts (login) to the peer so the
			// trail still records where they came from.
			entryCtx = adminauth.ContextWithPrincipal(request.Context(), adminauth.Principal{
				ClientIP: adminauth.ClientIP(request, trusted),
			})
		}
		recorder.Record(entryCtx, classifyAdminAction(request.URL.Path, request.Method),
			auditResource(request.URL.Path), auditTargetID(request.URL.Path),
			map[string]any{"method": request.Method, "status": status, "authenticated": authenticated})
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isMutating(request.Method) || !strings.HasPrefix(request.URL.Path, "/admin/api/") {
			next.ServeHTTP(writer, request)
			return
		}
		capture := &statusCaptureWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(capture, request)
		audit(writer, request, capture.status)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

var idSegment = regexp.MustCompile(`^[0-9]+$`)

// classifyAdminAction maps an admin request to a stable, groupable action
// name. Numeric IDs are replaced by ":id" so "nvidia_keys.delete" aggregates
// across keys; the specific value rides elsewhere (target ID). Unrecognized
// paths degrade to "admin.unknown" rather than panic.
func classifyAdminAction(path, method string) string {
	resource := auditResource(path)
	if resource == "" {
		return "admin.unknown"
	}
	name := strings.ReplaceAll(resource, "-", "_")
	segments := segmentsAfterResource(path)
	switch resource {
	case "settings", "proxy-pool":
		if method == http.MethodPatch || method == http.MethodPost {
			return name + ".update"
		}
	case "auth":
		if len(segments) == 1 {
			return "auth." + segments[0]
		}
	case "key-model-blocks":
		return "models.unblock"
	case "nvidia-keys":
		return nvidiaKeyAction(segments, method)
	case "access-keys":
		return accessKeyAction(segments, method)
	case "models":
		return modelAction(segments, method)
	}
	return name + "." + strings.ToLower(method)
}

func nvidiaKeyAction(segments []string, method string) string {
	if len(segments) == 0 {
		switch method {
		case http.MethodPost:
			return "nvidia_keys.import"
		}
		return "nvidia_keys." + strings.ToLower(method)
	}
	if len(segments) == 1 {
		if segments[0] == "batch" {
			return "nvidia_keys.import_batch"
		}
		if idSegment.MatchString(segments[0]) {
			switch method {
			case http.MethodPatch:
				return "nvidia_keys.update"
			case http.MethodDelete:
				return "nvidia_keys.delete"
			}
		}
		if segments[0] == "test-all" {
			return "nvidia_keys.test_all"
		}
	}
	if len(segments) == 2 && idSegment.MatchString(segments[0]) && segments[1] == "test" {
		return "nvidia_keys.test"
	}
	return "nvidia_keys." + strings.ToLower(method)
}

func accessKeyAction(segments []string, method string) string {
	if len(segments) == 0 {
		switch method {
		case http.MethodPost:
			return "access_keys.create"
		}
		return "access_keys." + strings.ToLower(method)
	}
	if len(segments) == 1 && idSegment.MatchString(segments[0]) {
		switch method {
		case http.MethodPatch:
			return "access_keys.update_policy"
		case http.MethodDelete:
			// A hard delete, not a revoke: the row is removed. Recording it as a
			// revoke made the trail claim the key still exists.
			return "access_keys.delete"
		}
	}
	if len(segments) == 2 && idSegment.MatchString(segments[0]) && segments[1] == "revoke" && method == http.MethodPost {
		return "access_keys.revoke"
	}
	return "access_keys." + strings.ToLower(method)
}

func modelAction(segments []string, method string) string {
	if len(segments) == 1 && segments[0] == "verify" {
		return "models.verify"
	}
	if len(segments) == 2 && segments[0] == "key-model-blocks" {
		return "models.unblock"
	}
	return "models." + strings.ToLower(method)
}

// auditResource is the first path segment after /admin/api/ ("nvidia-keys",
// "settings", ...). Empty for non-admin paths.
func auditResource(path string) string {
	const prefix = "/admin/api/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.TrimPrefix(trimmed, "/")
	if index := strings.IndexByte(trimmed, '/'); index >= 0 {
		return trimmed[:index]
	}
	return trimmed
}

// segmentsAfterResource returns the path segments following the resource, with
// empty/duplicate slashes collapsed.
func segmentsAfterResource(path string) []string {
	resource := auditResource(path)
	if resource == "" {
		return nil
	}
	const prefix = "/admin/api/"
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, resource)
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// auditTargetID returns the numeric ID embedded in a resource path when there
// is exactly one; empty otherwise ("batch"/action routes carry no single key).
func auditTargetID(path string) string {
	segments := segmentsAfterResource(path)
	if resource := auditResource(path); resource == "nvidia-keys" || resource == "access-keys" || resource == "models" {
		if len(segments) > 0 && idSegment.MatchString(segments[0]) {
			return segments[0]
		}
	}
	return ""
}

// statusCaptureWriter records the response status written by an inner handler
// so the audit entry can attach it without buffering the response body.
type statusCaptureWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCaptureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
