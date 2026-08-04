package web

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

type Handler struct {
	files      fs.FS
	fileServer http.Handler
	index      []byte
}

func NewEmbeddedHandler() (*Handler, error) {
	files, err := fs.Sub(embeddedFiles, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded frontend: %w", err)
	}
	return NewHandler(files)
}

func NewHandler(files fs.FS) (*Handler, error) {
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read frontend index: %w", err)
	}
	return &Handler{files: files, fileServer: http.FileServer(http.FS(files)), index: index}, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(writer.Header())
	if isReservedPath(request.URL.Path) {
		writer.Header().Set("Cache-Control", "no-store")
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if name != "." && name != "index.html" && isFile(h.files, name) {
		// Fingerprinted build outputs under /assets/* (Vite content-hashed
		// filenames) are immutable: the URL itself changes per build, so
		// clients may cache a copy for a year without revalidation. Anything
		// else kept short to avoid shipping stale scripts while the index
		// refreshes. The index document below stays no-store so changes to
		// its asset references take effect immediately.
		if strings.HasPrefix(name, "assets/") {
			writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			writer.Header().Set("Cache-Control", "no-cache")
		}
		h.fileServer.ServeHTTP(writer, request)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = writer.Write(h.index)
	}
}

func setSecurityHeaders(header http.Header) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Content-Security-Policy", contentSecurityPolicy)
}

func isReservedPath(requestPath string) bool {
	for _, prefix := range []string{"/v1", "/admin/api", "/health"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}

func isFile(files fs.FS, name string) bool {
	info, err := fs.Stat(files, name)
	return err == nil && !info.IsDir()
}
