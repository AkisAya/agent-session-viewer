package web

import "embed"

// FS holds the embedded static frontend assets.
//
//go:embed index.html app.js style.css
var FS embed.FS
