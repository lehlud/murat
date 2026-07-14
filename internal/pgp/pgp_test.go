package pgp

import (
	"strings"
	"testing"

	"lehnert.dev/murat/internal/protocol"
	"lehnert.dev/murat/internal/store"
)

func testConfigure(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	Configure(store.Paths{DataDir: dir, ConfigDir: dir, BodyDir: dir, RawDir: dir, PGPKeyFile: dir + "/pgp.enc"}, []byte("0123456789abcdef0123456789abcdef"))
}

func TestManagedKeyCreateAndExport(t *testing.T) {
	testConfigure(t)
	if err := Create("alice@example.com", "Alice"); err != nil {
		t.Fatal(err)
	}
	if !HasSecretKey("alice@example.com") || !HasPublicKey("alice@example.com") {
		t.Fatal("created key unavailable")
	}
	key, err := ExportPublicKey("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(key), "BEGIN PGP PUBLIC KEY BLOCK") {
		t.Fatalf("key = %q", key)
	}
}

func TestApplyDraftEncryptAndSign(t *testing.T) {
	testConfigure(t)
	if err := Create("alice@example.com", "Alice"); err != nil {
		t.Fatal(err)
	}
	if err := Create("bob@example.com", "Bob"); err != nil {
		t.Fatal(err)
	}
	draft, status, err := ApplyDraft("alice@example.com", protocol.Draft{From: "alice@example.com", To: "bob@example.com", Body: "hello", PGP: "encrypt,sign"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Body, "BEGIN PGP MESSAGE") || !strings.Contains(status, "encrypted") || !strings.Contains(status, "signed") {
		t.Fatalf("draft=%#v status=%q", draft, status)
	}
	text, verify, ok := ProcessText(draft.Body)
	if !ok || text != "hello" || !strings.Contains(verify, "decrypted") {
		t.Fatalf("process=%q %q %v", text, verify, ok)
	}
}

func TestPublicKeyAttachment(t *testing.T) {
	if !IsPublicKeyAttachment("key.asc", "application/pgp-keys", []byte("x")) {
		t.Fatal("public key attachment not detected")
	}
}
