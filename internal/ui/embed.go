package ui

import "embed"

// Dist holds the built SPA assets under dist/.
//
//go:embed all:dist
var Dist embed.FS
