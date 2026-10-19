package web

import "embed"

//go:embed index.html app.css app.js window.js
var Files embed.FS
