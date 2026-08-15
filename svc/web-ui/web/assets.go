// Package web owns the Web UI static artifact embedded in the service binary.
package web

import "embed"

// Files contains the browser entry point.
//
//go:embed index.html
var Files embed.FS
