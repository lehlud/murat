package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAttachments(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	s, err := Open(paths, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: x\r\nDate: Tue, 09 Jun 2026 10:00:00 +0000\r\nContent-Type: multipart/mixed; boundary=abc\r\n\r\n--abc\r\nContent-Type: text/plain\r\n\r\nhello\r\n--abc\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=note.txt\r\nContent-Transfer-Encoding: base64\r\n\r\naGVsbG8gYXR0YWNobWVudA==\r\n--abc--\r\n")
	msg, err := s.ImportRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !msg.HasAttachment {
		t.Fatal("expected attachment marker")
	}
	saveDir := filepath.Join(dir, "out")
	if err := os.Mkdir(saveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pathsOut, err := s.SaveAttachments(msg, saveDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pathsOut) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(pathsOut))
	}
	data, err := os.ReadFile(pathsOut[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello attachment" {
		t.Fatalf("unexpected attachment body: %q", string(data))
	}
}

func TestSearchIndexImportPersistRemove(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	key := bytes.Repeat([]byte{2}, 32)
	s, err := Open(paths, key)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: unique-search-subject\r\nDate: Tue, 09 Jun 2026 10:00:00 +0000\r\nContent-Type: text/plain\r\n\r\nunique-body-token\r\n")
	msg, err := s.ImportRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Search("unique-body-token", false, false, false); len(got) != 1 {
		t.Fatalf("search after import got %d", len(got))
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(paths, key)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Search("unique-search-subject", false, false, false); len(got) != 1 {
		t.Fatalf("search after reopen got %d", len(got))
	}
	if err := reopened.RemoveMessage(msg.Key, true); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Search("unique-search-subject", false, false, false); len(got) != 0 {
		t.Fatalf("search after remove got %d", len(got))
	}
}

func TestHTMLToRichText(t *testing.T) {
	got := htmlToRichText(`<style>no</style><p>Hello <strong>bold</strong><br><em>it</em></p><ul><li>one</li><li><span style="text-decoration: underline">two</span></li></ul>`)
	want := "Hello **bold**\n*it*\n- one\n- __two__"
	if got != want {
		t.Fatalf("htmlToRichText() = %q, want %q", got, want)
	}
}

func TestDecodeWindows1252Body(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: cp1252\r\nContent-Type: text/plain; charset=windows-1252\r\n\r\n\x93hello\x94 \x80\r\n")
	_, body, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if body != "“hello” €" {
		t.Fatalf("body = %q", body)
	}
}

func TestDecodeMIMEEncodedSubject(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: =?utf-8?Q?[StudOn]_3._online-=C3=9Cbung_bis_zum_22._Juni_(22?= =?utf-8?Q?:00)?=\r\nDate: Tue, 09 Jun 2026 11:57:47 +0000\r\n\r\nbody\r\n")
	parsed, _, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	msg := messageFromMail("key", parsed, raw, "key.eml.enc", false)
	if msg.Subject != "[StudOn] 3. online-Übung bis zum 22. Juni (22:00)" {
		t.Fatalf("subject = %q", msg.Subject)
	}
}

func TestNormalizeDecodesStoredMIMEEncodedSubject(t *testing.T) {
	s := &Store{index: emptyIndex()}
	s.index.Messages["m"] = &Message{Key: "m", Subject: "=?utf-8?Q?[StudOn]_3._online-=C3=9Cbung_bis_zum_22._Juni_(22?= =?utf-8?Q?:00)?="}
	s.normalize()
	if got := s.index.Messages["m"].Subject; got != "[StudOn] 3. online-Übung bis zum 22. Juni (22:00)" {
		t.Fatalf("subject = %q", got)
	}
	if !s.dirty {
		t.Fatal("expected store dirty after subject migration")
	}
}

func TestKnownRemoteIDs(t *testing.T) {
	s := &Store{index: emptyIndex()}
	s.index.Messages["a"] = &Message{AccountID: "acct", RemoteID: "imap:INBOX:1"}
	s.index.Messages["b"] = &Message{AccountID: "other", RemoteID: "imap:INBOX:2"}
	known := s.KnownRemoteIDs("acct")
	if !known["imap:INBOX:1"] {
		t.Fatal("expected account remote id")
	}
	if known["imap:INBOX:2"] {
		t.Fatal("unexpected other account remote id")
	}
}

func TestKnownAddressesFromImport(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	s, err := Open(paths, bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: Alice <alice@example.com>\r\nTo: Bob <bob@example.com>\r\nCc: carol@example.com\r\nBcc: Dan <dan@example.com>\r\nSubject: x\r\n\r\nbody\r\n")
	if _, err := s.ImportRaw(raw); err != nil {
		t.Fatal(err)
	}
	known := map[string]KnownAddress{}
	for _, addr := range s.KnownAddresses() {
		known[addr.Email] = addr
	}
	for _, email := range []string{"alice@example.com", "bob@example.com", "carol@example.com", "dan@example.com"} {
		if known[email].Email == "" {
			t.Fatalf("missing known address %s", email)
		}
	}
	if known["alice@example.com"].Name != "Alice" {
		t.Fatalf("alice name = %q", known["alice@example.com"].Name)
	}
}

func TestFolderColumnResolvesMailboxIDs(t *testing.T) {
	s := &Store{index: emptyIndex()}
	s.index.Mailboxes["acct"] = []any{
		map[string]any{"id": "a", "name": "INBOX", "role": "inbox"},
		map[string]any{"id": "b", "name": "Archive", "role": ""},
		map[string]any{"id": "c", "name": "2026", "parentId": "b"},
	}
	msg := &Message{store: s, AccountID: "acct", Tags: []string{"a"}}
	if got := msg.FolderColumn(); got != "i" {
		t.Fatalf("inbox marker = %q", got)
	}
	msg.Tags = []string{"c"}
	if got := msg.FolderColumn(); got != "2026" {
		t.Fatalf("basename marker = %q", got)
	}
}

func testPaths(dir string) Paths {
	return Paths{
		ConfigDir:    filepath.Join(dir, "config"),
		ConfigFile:   filepath.Join(dir, "config", "config.toml"),
		DataDir:      filepath.Join(dir, "data"),
		KeyFile:      filepath.Join(dir, "data", "key.gpg"),
		IndexFile:    filepath.Join(dir, "data", "mail.enc.json"),
		AccountsFile: filepath.Join(dir, "data", "accounts.enc.json"),
		SearchFile:   filepath.Join(dir, "data", "search.enc.json"),
		BodyDir:      filepath.Join(dir, "data", "eml"),
		RawDir:       filepath.Join(dir, "data", "eml"),
	}
}
