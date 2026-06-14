package compose

import (
	"os"
	"path/filepath"
	"testing"

	"lehnert.dev/murat/internal/protocol"
)

func TestDraftFromRoundTrip(t *testing.T) {
	path, err := WriteDraftFile(protocol.Draft{From: "Alice <alice@example.com>", To: "bob@example.com", Subject: "hello", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	draft, err := ReadDraftFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if draft.From != "Alice <alice@example.com>" {
		t.Fatalf("from = %q", draft.From)
	}
}

func TestMuratPathDirCreatesMuratSymlink(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "custom-name")
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	pathDir, cleanup := muratPathDir(exe)
	defer cleanup()
	link := filepath.Join(pathDir, "murat")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if target != exe {
		t.Fatalf("link target = %q, want %q", target, exe)
	}
}
