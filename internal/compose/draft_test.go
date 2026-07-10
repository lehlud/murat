package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lehnert.dev/murat/internal/protocol"
)

func TestDraftFromRoundTrip(t *testing.T) {
	path, err := WriteDraftFile(protocol.Draft{From: "Alice <alice@example.com>", To: "bob@example.com", Subject: "hello", Body: "body", PGP: "sign"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PGP:") {
		t.Fatalf("draft file exposes PGP header:\n%s", string(data))
	}
	draft, err := ReadDraftFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if draft.From != "Alice <alice@example.com>" {
		t.Fatalf("from = %q", draft.From)
	}
	if draft.PGP != "" {
		t.Fatalf("pgp = %q", draft.PGP)
	}
}

func TestEditWithEditorPreservesHiddenPGPOptions(t *testing.T) {
	draft, err := EditWithEditor(protocol.Draft{From: "alice@example.com", To: "bob@example.com", PGP: "sign", Body: "body"}, "true")
	if err != nil {
		t.Fatal(err)
	}
	if draft.PGP != "sign" {
		t.Fatalf("pgp = %q", draft.PGP)
	}
}

func TestReadDraftFileAcceptsLegacyPGPHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.txt")
	if err := os.WriteFile(path, []byte("From: alice@example.com\nPGP: sign\n\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft, err := ReadDraftFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if draft.PGP != "sign" {
		t.Fatalf("pgp = %q", draft.PGP)
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
