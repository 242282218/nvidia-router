package web

import "embed"

// embeddedFiles contains the production frontend built by Vite.
//
//go:embed all:dist
var embeddedFiles embed.FS
