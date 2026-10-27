// Package web contains the self-hosted administration interface.
package web

import (
	"embed"
	"io/fs"
)

//go:embed assets/*
var assets embed.FS

// Assets returns the administration assets rooted at index.html.
func Assets() fs.FS {
	root, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	return root
}

// FS is an alias for Assets.
func FS() fs.FS { return Assets() }
