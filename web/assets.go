// Package web provides embedded web assets for the wisp remote access client.
//
// The dist/ directory is embedded at build time. During development,
// if dist/ exists on the filesystem, it will be used instead, allowing
// for hot reloading of the web client.
package web

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

// assets holds the embedded web client files from dist/.
// When the web client is built (npm run build), these files
// are embedded into the binary.
//
//go:embed dist/*
var assets embed.FS

// GetAssets returns a filesystem containing the web client assets.
// In development mode (when the dist directory exists on the filesystem
// relative to the working directory), it returns the live filesystem
// for hot reloading. In production, it returns the embedded assets.
//
// The devPath parameter specifies the path to check for development mode.
// If empty, it defaults to "./web/dist" (relative to the working directory).
func GetAssets(devPath string) fs.FS {
	if devPath == "" {
		devPath = "./web/dist"
	}

	// Development: check filesystem first for hot reloading
	if stat, err := os.Stat(devPath); err == nil && stat.IsDir() {
		return os.DirFS(devPath)
	}

	// Production: use embedded assets
	// The assets FS has "dist/" prefix, so we need to use Sub
	subFS, err := fs.Sub(assets, "dist")
	if err != nil {
		// This should never happen with properly embedded assets
		panic("failed to access embedded web assets: " + err.Error())
	}
	return subFS
}

// GetAssetsWithBase returns a filesystem for assets, checking for development
// mode at a path relative to the given base directory.
func GetAssetsWithBase(baseDir string) fs.FS {
	devPath := filepath.Join(baseDir, "web", "dist")
	return GetAssets(devPath)
}
