package web

import "embed"

//go:embed index.html manifest.json sw.js static
var FS embed.FS
