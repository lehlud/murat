package userdirs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadsUsesExistingXDGDownloadEnv(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "xdg-downloads")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DOWNLOAD_DIR", dir)

	if got := Downloads(); got != dir {
		t.Fatalf("Downloads() = %q, want %q", got, dir)
	}
}

func TestDownloadsUsesUserDirsFile(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".config")
	dir := filepath.Join(home, "My Downloads")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := "XDG_DESKTOP_DIR=\"$HOME/Desktop\"\nXDG_DOWNLOAD_DIR=\"$HOME/My Downloads\"\n"
	if err := os.WriteFile(filepath.Join(config, "user-dirs.dirs"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_DOWNLOAD_DIR", "")

	if got := Downloads(); got != dir {
		t.Fatalf("Downloads() = %q, want %q", got, dir)
	}
}

func TestDownloadsFallsBackToHomeDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "missing-config"))
	t.Setenv("XDG_DOWNLOAD_DIR", "")

	want := filepath.Join(home, "Downloads")
	if got := Downloads(); got != want {
		t.Fatalf("Downloads() = %q, want %q", got, want)
	}
}

func TestCacheUsesXDGCacheHome(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("XDG_CACHE_HOME", dir)

	if got := Cache(); got != dir {
		t.Fatalf("Cache() = %q, want %q", got, dir)
	}
}

func TestCacheFallsBackToHomeCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")

	want := filepath.Join(home, ".cache")
	if got := Cache(); got != want {
		t.Fatalf("Cache() = %q, want %q", got, want)
	}
}
