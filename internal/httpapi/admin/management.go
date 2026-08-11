package admin

import "net/http"

func NewManagement(nvidiaKeys, accessKeys, models, proxyPool, auditLogs http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/admin/api/nvidia-keys", nvidiaKeys)
	mux.Handle("/admin/api/nvidia-keys/", nvidiaKeys)
	mux.Handle("/admin/api/access-keys", accessKeys)
	mux.Handle("/admin/api/access-keys/", accessKeys)
	mux.Handle("/admin/api/models", models)
	mux.Handle("/admin/api/models/", models)
	mux.Handle("/admin/api/key-model-blocks/", models)
	mux.Handle("/admin/api/proxy-pool", proxyPool)
	mux.Handle("/admin/api/audit-logs", auditLogs)
	return mux
}
