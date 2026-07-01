package app

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lehnert.dev/murat/internal/store"
)

func TestWriteExportTarIncludesExpectedFiles(t *testing.T) {
	data := exportData{
		Accounts: []store.Account{{
			ID:       "acct",
			Email:    "me@example.com",
			Protocol: "imap",
			Secret:   "account-secret",
		}},
		KnownAddresses: []store.KnownAddress{{Name: "Alice", Email: "alice@example.com", Count: 3}},
		PublicKeys:     []byte("public keys"),
		SecretKeys:     []byte("secret keys"),
		OwnerTrust:     []byte("owner trust"),
	}
	var buf bytes.Buffer
	if err := writeExportTarAt(&buf, data, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	files := readTarFiles(t, buf.Bytes())
	for _, name := range []string{"manifest.json", "accounts.json", "known-addresses.json", "gpg/public.asc", "gpg/secret.asc", "gpg/ownertrust.txt"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("missing tar file %s", name)
		}
	}
	if got := string(files["gpg/public.asc"]); got != "public keys" {
		t.Fatalf("public keys = %q", got)
	}
	if got := string(files["gpg/secret.asc"]); got != "secret keys" {
		t.Fatalf("secret keys = %q", got)
	}

	var accounts store.AccountIndex
	if err := json.Unmarshal(files["accounts.json"], &accounts); err != nil {
		t.Fatal(err)
	}
	if len(accounts.Accounts) != 1 || accounts.Accounts[0].Secret != "account-secret" {
		t.Fatalf("accounts = %#v", accounts.Accounts)
	}

	var manifest exportManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Accounts != 1 || manifest.KnownAddresses != 1 || manifest.SecretKeyBytes != len(data.SecretKeys) {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestCmdExportRefusesOverwriteBeforeCollectingData(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "backup.tar.gpg")
	if err := os.WriteFile(output, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldCollect := collectExportData
	oldEncrypt := encryptExportData
	t.Cleanup(func() {
		collectExportData = oldCollect
		encryptExportData = oldEncrypt
	})

	called := false
	collectExportData = func() (exportData, error) {
		called = true
		return exportData{}, nil
	}
	encryptExportData = func(string, bool, exportData) error {
		t.Fatal("encryptExportData should not be called")
		return nil
	}

	err := cmdExport([]string{output})
	if err == nil || !strings.Contains(err.Error(), "output exists") {
		t.Fatalf("cmdExport error = %v", err)
	}
	if called {
		t.Fatal("collectExportData was called")
	}
}

func TestCmdExportPassesDataToEncryptor(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "backup.tar.gpg")
	data := exportData{
		Accounts:       []store.Account{{ID: "acct", Email: "me@example.com", Secret: "secret"}},
		KnownAddresses: []store.KnownAddress{{Email: "alice@example.com"}},
		PublicKeys:     []byte("pub"),
		SecretKeys:     []byte("sec"),
		OwnerTrust:     []byte("trust"),
	}

	oldCollect := collectExportData
	oldEncrypt := encryptExportData
	t.Cleanup(func() {
		collectExportData = oldCollect
		encryptExportData = oldEncrypt
	})

	collectExportData = func() (exportData, error) { return data, nil }
	encryptExportData = func(path string, force bool, got exportData) error {
		if path != output {
			t.Fatalf("output path = %q", path)
		}
		if force {
			t.Fatal("force = true")
		}
		if len(got.Accounts) != 1 || got.Accounts[0].Secret != "secret" {
			t.Fatalf("export data = %#v", got)
		}
		var buf bytes.Buffer
		if err := writeExportTarAt(&buf, got, time.Unix(0, 0).UTC()); err != nil {
			return err
		}
		return os.WriteFile(path, buf.Bytes(), 0o600)
	}

	if err := cmdExport([]string{output}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
}

func TestGPGSymmetricEncryptArgsUseLoopbackPassphraseFD(t *testing.T) {
	args := strings.Join(gpgSymmetricEncryptArgs("out.gpg"), " ")
	for _, want := range []string{"--pinentry-mode loopback", "--passphrase-fd 3", "--symmetric", "--cipher-algo AES256", "--output out.gpg"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
}

func TestPassphraseFDWritesPassphraseWithNewline(t *testing.T) {
	r, cleanup, err := passphraseFD([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret\n" {
		t.Fatalf("passphrase pipe = %q", got)
	}
}

func readTarFiles(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(data))
	files := map[string][]byte{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = body
	}
	return files
}
