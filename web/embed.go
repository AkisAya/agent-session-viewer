package web

import "embed"

// FS holds the embedded static frontend assets.
//
// The frontend is plain ES modules (no build step): app.js imports the other
// *.js modules, which the browser fetches from this same embedded FS.
//
//go:embed index.html *.js style.css logo.svg
var FS embed.FS
