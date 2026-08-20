package admin

import "net/http"

func NewManagement(nvidiaKeys, accessKeys, models, proxyPool, auditLogs, providers http.Handler, additional ...http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/admin/api/nvidia-keys", nvidiaKeys)
	mux.Handle("/admin/api/nvidia-keys/", nvidiaKeys)
	mux.Handle("/admin/api/access-keys", accessKeys)
	mux.Handle("/admin/api/access-keys/", accessKeys)
	mux.Handle("/admin/api/models", models)
	mux.Handle("/admin/api/models/", models)
	mux.Handle("/admin/api/key-model-blocks/", models)
	mux.Handle("/admin/api/proxy-pool", proxyPool)
	mux.Handle("/admin/api/proxy-pool/", proxyPool)
	mux.Handle("/admin/api/audit-logs", auditLogs)
	mux.Handle("/admin/api/providers", providers)
	mux.Handle("/admin/api/providers/", providers)
	if len(additional) > 0 && additional[0] != nil {
		mux.Handle("/admin/api/model-test-jobs", additional[0])
		mux.Handle("/admin/api/model-test-jobs/", additional[0])
	}
	if len(additional) > 1 && additional[1] != nil {
		mux.Handle("/admin/api/model-health/", additional[1])
	}
	return mux
}
