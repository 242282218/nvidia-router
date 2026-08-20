package admin

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"nvidia-router/internal/adminaudit"
	"nvidia-router/internal/adminauth"
)

func TestAuditRecordSurvivesRequestCancellation(t *testing.T) {
	repository := &canceledAuditRepository{}
	recorder := adminaudit.NewRecorder(repository, slog.Default())
	handler := AuditMiddleware(recorder, []*net.IPNet{}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/admin/api/settings", nil)
	request = request.WithContext(adminauth.ContextWithPrincipal(request.Context(), adminauth.Principal{SessionID: "session", ClientIP: "192.0.2.1"}))
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	cancel()

	handler.ServeHTTP(httptest.NewRecorder(), request)
	if repository.ctxErr != nil {
		t.Fatal(repository.ctxErr)
	}
	if repository.entry.SessionID == nil || *repository.entry.SessionID != "session" {
		t.Fatalf("audit session ID = %v, want session", repository.entry.SessionID)
	}
	if repository.entry.ClientIP != "192.0.2.1" {
		t.Fatalf("audit client IP = %q, want 192.0.2.1", repository.entry.ClientIP)
	}
}

type canceledAuditRepository struct {
	ctxErr error
	entry  adminaudit.Entry
}

func (r *canceledAuditRepository) Insert(ctx context.Context, entry adminaudit.Entry) (int64, error) {
	r.ctxErr = ctx.Err()
	r.entry = entry
	return 1, nil
}

func TestClassifyAdminAction(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		method string
		want   string
	}{
		{"nvidia key import", "/admin/api/nvidia-keys", "POST", "nvidia_keys.import"},
		{"nvidia key list", "/admin/api/nvidia-keys", "GET", "nvidia_keys.get"},
		{"nvidia key batch", "/admin/api/nvidia-keys/batch", "POST", "nvidia_keys.import_batch"},
		{"nvidia key update", "/admin/api/nvidia-keys/7", "PATCH", "nvidia_keys.update"},
		{"nvidia key delete", "/admin/api/nvidia-keys/7", "DELETE", "nvidia_keys.delete"},
		{"nvidia key test", "/admin/api/nvidia-keys/7/test", "POST", "nvidia_keys.test"},
		{"nvidia key test all", "/admin/api/nvidia-keys/test-all", "POST", "nvidia_keys.test_all"},
		{"access key create", "/admin/api/access-keys", "POST", "access_keys.create"},
		{"access key policy", "/admin/api/access-keys/3", "PATCH", "access_keys.update_policy"},
		{"access key delete", "/admin/api/access-keys/3", "DELETE", "access_keys.delete"},
		{"access key revoke", "/admin/api/access-keys/3/revoke", "POST", "access_keys.revoke"},
		{"settings update", "/admin/api/settings", "PATCH", "settings.update"},
		{"proxy pool update", "/admin/api/proxy-pool", "PATCH", "proxy_pool.update"},
		{"models verify", "/admin/api/models/verify", "POST", "models.verify"},
		{"models unblock", "/admin/api/key-model-blocks/4/2", "DELETE", "models.unblock"},
		{"auth login", "/admin/api/auth/login", "POST", "auth.login"},
		{"auth logout", "/admin/api/auth/logout", "POST", "auth.logout"},
		{"unknown resource", "/admin/api/whatever", "POST", "whatever.post"},
		{"non admin unknown", "/v1/chat/completions", "POST", "admin.unknown"},
		{"empty path", "", "POST", "admin.unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyAdminAction(test.path, test.method); got != test.want {
				t.Fatalf("classifyAdminAction(%q, %q) = %q, want %q", test.path, test.method, got, test.want)
			}
		})
	}
}

func TestAuditResourceAndTargetID(t *testing.T) {
	tests := []struct {
		path     string
		resource string
		targetID string
	}{
		{"/admin/api/nvidia-keys", "nvidia-keys", ""},
		{"/admin/api/nvidia-keys/12", "nvidia-keys", "12"},
		{"/admin/api/nvidia-keys/12/test", "nvidia-keys", "12"},
		{"/admin/api/access-keys/9", "access-keys", "9"},
		{"/admin/api/settings", "settings", ""},
		{"/v1/chat/completions", "", ""},
	}
	for _, test := range tests {
		if got := auditResource(test.path); got != test.resource {
			t.Fatalf("auditResource(%q) = %q, want %q", test.path, got, test.resource)
		}
		if got := auditTargetID(test.path); got != test.targetID {
			t.Fatalf("auditTargetID(%q) = %q, want %q", test.path, got, test.targetID)
		}
	}
}

func TestIsMutating(t *testing.T) {
	if !isMutating("POST") || !isMutating("PATCH") || !isMutating("PUT") || !isMutating("DELETE") {
		t.Fatal("mutating methods not detected")
	}
	if isMutating("GET") || isMutating("HEAD") || isMutating("OPTIONS") {
		t.Fatal("non-mutating method classified as mutating")
	}
}
