package web

import (
	"io/fs"
	"testing"
)

func TestGetAssets(t *testing.T) {
	// Test that GetAssets returns a valid filesystem
	assets := GetAssets("")
	if assets == nil {
		t.Fatal("GetAssets returned nil")
	}

	// Verify we can read the embedded index.html
	file, err := assets.Open("index.html")
	if err != nil {
		t.Fatalf("failed to open index.html: %v", err)
	}
	defer file.Close()

	// Verify it's a file, not a directory
	stat, err := file.Stat()
	if err != nil {
		t.Fatalf("failed to stat index.html: %v", err)
	}
	if stat.IsDir() {
		t.Error("index.html is a directory, expected file")
	}
	if stat.Size() == 0 {
		t.Error("index.html is empty")
	}
}

func TestGetAssetsWithBase(t *testing.T) {
	// Test GetAssetsWithBase with a non-existent path falls back to embedded
	assets := GetAssetsWithBase("/nonexistent/path")
	if assets == nil {
		t.Fatal("GetAssetsWithBase returned nil")
	}

	// Should still work with embedded assets
	file, err := assets.Open("index.html")
	if err != nil {
		t.Fatalf("failed to open index.html: %v", err)
	}
	file.Close()
}

func TestAssetsFSInterface(t *testing.T) {
	// Verify GetAssets returns a proper fs.FS implementation
	var assets fs.FS = GetAssets("")

	// Test that we can walk the filesystem
	var fileCount int
	err := fs.WalkDir(assets, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fileCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk assets: %v", err)
	}
	if fileCount == 0 {
		t.Error("no files found in assets")
	}
}
