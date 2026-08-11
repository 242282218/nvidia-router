package admin

import "testing"

func TestClassifyAdminAction(t *testing.T) {
	tests := []struct {
		name string
		path string
		method string
		want string
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
		{"access key revoke", "/admin/api/access-keys/3", "DELETE", "access_keys.revoke"},
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
		path       string
		resource   string
		targetID   string
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
