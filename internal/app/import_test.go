package app

import (
	"bytes"
	"testing"
	"time"

	"lehnert.dev/murat/internal/store"
)

func TestReadImportTarReadsExportArchive(t *testing.T) {
	data := exportData{
		Accounts:       []store.Account{{ID: "acct", Email: "me@example.com", Secret: "secret"}},
		KnownAddresses: []store.KnownAddress{{Name: "Alice", Email: "alice@example.com", Count: 2}},
		PublicKeys:     []byte("public"),
		SecretKeys:     []byte("secret"),
		OwnerTrust:     []byte("trust"),
	}
	var buf bytes.Buffer
	if err := writeExportTarAt(&buf, data, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := readImportTar(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 1 || got.Accounts[0].Secret != "secret" {
		t.Fatalf("accounts = %#v", got.Accounts)
	}
	if len(got.KnownAddresses) != 1 || got.KnownAddresses[0].Email != "alice@example.com" {
		t.Fatalf("known addresses = %#v", got.KnownAddresses)
	}
	if string(got.PublicKeys) != "public" || string(got.SecretKeys) != "secret" || string(got.OwnerTrust) != "trust" {
		t.Fatalf("gpg data = %q %q %q", got.PublicKeys, got.SecretKeys, got.OwnerTrust)
	}
}

func TestCmdImportPassesOptions(t *testing.T) {
	oldRead := readImportData
	oldApply := applyImportData
	t.Cleanup(func() {
		readImportData = oldRead
		applyImportData = oldApply
	})

	readPath := ""
	readImportData = func(path string) (exportData, error) {
		readPath = path
		return exportData{Accounts: []store.Account{{ID: "acct"}}}, nil
	}
	applyImportData = func(data exportData, options importOptions) (importSummary, error) {
		if len(data.Accounts) != 1 || data.Accounts[0].ID != "acct" {
			t.Fatalf("data = %#v", data)
		}
		if options.GPGRecipient != "me@example.com" {
			t.Fatalf("gpg recipient = %q", options.GPGRecipient)
		}
		if !options.ReplaceAccounts {
			t.Fatal("replace accounts = false")
		}
		return importSummary{Accounts: 1}, nil
	}

	if err := cmdImportArchive([]string{"--gpg-key", "me@example.com", "--replace-accounts", "backup.tar.gpg"}); err != nil {
		t.Fatal(err)
	}
	if readPath != "backup.tar.gpg" {
		t.Fatalf("read path = %q", readPath)
	}
}

func TestMergeImportedAccountsReplacesByID(t *testing.T) {
	existing := []store.Account{
		{ID: "one", Email: "old@example.com", Secret: "old"},
		{ID: "two", Email: "two@example.com", Secret: "two"},
	}
	imported := []store.Account{
		{ID: "one", Email: "new@example.com", Secret: "new"},
		{ID: "three", Email: "three@example.com", Secret: "three"},
	}
	got := mergeImportedAccounts(existing, imported)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Email != "new@example.com" || got[0].Secret != "new" {
		t.Fatalf("replacement = %#v", got[0])
	}
	if got[2].ID != "three" {
		t.Fatalf("appended = %#v", got[2])
	}
}
