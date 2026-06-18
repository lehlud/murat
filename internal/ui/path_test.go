package ui

import (
	"os"
	"path/filepath"
	"testing"

	"lehnert.dev/murat/internal/userdirs"
)

func TestCompleteDirectoryPathUsesFallbackForEmptyInput(t *testing.T) {
	dir := t.TempDir()
	want := dir + string(os.PathSeparator)
	if got := completeDirectoryPath("", dir); got != want {
		t.Fatalf("completeDirectoryPath() = %q, want %q", got, want)
	}
}

func TestCompleteDirectoryPathCompletesDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Downloads")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Download.txt"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := filepath.Join(root, "Down")
	want := dir + string(os.PathSeparator)
	if got := completeDirectoryPath(input, ""); got != want {
		t.Fatalf("completeDirectoryPath() = %q, want %q", got, want)
	}
}

func TestCompleteFilePathCompletesFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := filepath.Join(root, "not")
	if got := completeFilePath(input, ""); got != path {
		t.Fatalf("completeFilePath() = %q, want %q", got, path)
	}
}

func TestCompleteFilePathKeepsDirectorySlash(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	input := filepath.Join(root, "nes")
	want := dir + string(os.PathSeparator)
	if got := completeFilePath(input, ""); got != want {
		t.Fatalf("completeFilePath() = %q, want %q", got, want)
	}
}

func TestOpenAttachmentDirUsesCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	t.Setenv("XDG_CACHE_HOME", cache)
	app := &App{}
	dir, err := app.openAttachmentDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Dir(dir), filepath.Join(userdirs.Cache(), "murat", "attachments"); got != want {
		t.Fatalf("parent = %q, want %q", got, want)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openedDirs = []string{dir}
	app.cleanupOpenedAttachments()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir still exists: %v", err)
	}
}
