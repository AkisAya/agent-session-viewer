package web

import "embed"

// FS holds the embedded static frontend assets.
//
//go:embed index.html app.js md.js style.css logo.svg
var FS embed.FS
