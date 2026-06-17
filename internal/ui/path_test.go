package ui

import (
	"os"
	"path/filepath"
	"testing"
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
