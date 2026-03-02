//go:build dev

package api

import "embed"

// WebAssets is empty in dev mode — frontend is served externally (e.g., Vite dev server).
var WebAssets embed.FS
