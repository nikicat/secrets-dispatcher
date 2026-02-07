package api

import "embed"

// WebAssets contains the compiled frontend assets from web/dist/.
// This is populated at build time by the Go compiler.
//
//go:embed web/dist/*
var WebAssets embed.FS
