package ui

import "embed"

// DistFS embeds the built React static files.
//
//go:embed dist/* dist/assets/*
var DistFS embed.FS
