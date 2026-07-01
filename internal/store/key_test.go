package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureRawKeyWritesLoadableKey(t *testing.T) {
	paths := testKeyPaths(t)
	key, err := EnsureRawKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d", len(key))
	}

	loaded, err := LoadKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, key) {
		t.Fatalf("loaded key differs")
	}

	data, err := os.ReadFile(paths.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	var file keyFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Version != 1 || file.Kind != "raw" || file.Key == "" {
		t.Fatalf("key file = %#v", file)
	}

	info, err := os.Stat(paths.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key file mode = %o", mode)
	}
}

func TestEnsureRawKeyKeepsExistingLoadableKey(t *testing.T) {
	paths := testKeyPaths(t)
	first, err := EnsureRawKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureRawKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("EnsureRawKey replaced an existing loadable key")
	}
}

func testKeyPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	return Paths{
		ConfigDir: filepath.Join(dir, "config"),
		DataDir:   dataDir,
		KeyFile:   filepath.Join(dataDir, "key.gpg"),
		BodyDir:   filepath.Join(dataDir, "eml"),
		RawDir:    filepath.Join(dataDir, "eml"),
	}
}

func TestGPGErrorMessageUsesFirstStderrLine(t *testing.T) {
	got := gpgErrorMessage("\nfirst\nsecond\n", os.ErrNotExist)
	if got != "first" {
		t.Fatalf("message = %q", got)
	}
}
