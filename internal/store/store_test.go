package store

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
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

func TestInlineImageAppearsAsAttachment(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	s, err := Open(paths, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: inline\r\nDate: Tue, 09 Jun 2026 10:00:00 +0000\r\nContent-Type: multipart/related; boundary=rel\r\n\r\n--rel\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>logo <img src=\"cid:logo@example\"> <img src=\"cid:photo@example\"></p>\r\n--rel\r\nContent-Type: image/png\r\nContent-Disposition: inline\r\nContent-ID: <logo@example>\r\nContent-Transfer-Encoding: base64\r\n\r\nAQID\r\n--rel\r\nContent-Type: image/jpeg; name=photo.jpg\r\nContent-Disposition: inline\r\nContent-ID: <photo@example>\r\nContent-Transfer-Encoding: base64\r\n\r\nBAUG\r\n--rel--\r\n")
	msg, err := s.ImportRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !msg.HasAttachment {
		t.Fatal("expected inline image attachment marker")
	}
	attachments, err := s.Attachments(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(attachments))
	}
	if attachments[0].Filename != "logo@example.png" {
		t.Fatalf("filename = %q", attachments[0].Filename)
	}
	if attachments[0].ContentType != "image/png" {
		t.Fatalf("content type = %q", attachments[0].ContentType)
	}
	if !bytes.Equal(attachments[0].Data, []byte{1, 2, 3}) {
		t.Fatalf("data = %#v", attachments[0].Data)
	}
	if attachments[1].Filename != "photo.jpg" {
		t.Fatalf("named filename = %q", attachments[1].Filename)
	}
	if attachments[1].ContentType != "image/jpeg" {
		t.Fatalf("named content type = %q", attachments[1].ContentType)
	}
	if !bytes.Equal(attachments[1].Data, []byte{4, 5, 6}) {
		t.Fatalf("named data = %#v", attachments[1].Data)
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

func TestTrashListsTrashedMessages(t *testing.T) {
	s, err := Open(testPaths(t.TempDir()), bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.ImportRaw([]byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: trash\r\nDate: Tue, 09 Jun 2026 10:00:00 +0000\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	msg.MarkTrashed()
	if got := s.Messages(false); len(got) != 0 {
		t.Fatalf("Messages(false) len = %d, want 0", len(got))
	}
	if got := s.Trash(); len(got) != 1 || got[0].Key != msg.Key {
		t.Fatalf("Trash() = %#v", got)
	}
}

func TestReadAndFolderDirtyState(t *testing.T) {
	s, err := Open(testPaths(t.TempDir()), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.ImportRaw([]byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: dirty\r\nDate: Tue, 09 Jun 2026 10:00:00 +0000\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	msg.SetRead(true)
	if !msg.ReadDirty {
		t.Fatal("SetRead should mark read dirty")
	}
	msg.SetReadSynced(false)
	if msg.Read || msg.ReadDirty {
		t.Fatalf("SetReadSynced read=%v dirty=%v", msg.Read, msg.ReadDirty)
	}
	msg.SetSpam(true)
	if !msg.FolderDirty || !msg.IsSpam() {
		t.Fatalf("SetSpam dirty=%v spam=%v", msg.FolderDirty, msg.IsSpam())
	}
	msg.SetFolderSynced([]string{"INBOX"}, false, false)
	if msg.FolderDirty || msg.IsSpam() {
		t.Fatalf("SetFolderSynced dirty=%v spam=%v", msg.FolderDirty, msg.IsSpam())
	}
}

func TestSetSpamFalseRemovesResolvedSpamTags(t *testing.T) {
	s := &Store{index: emptyIndex(), search: map[string][]string{}}
	s.index.Mailboxes["acct"] = []any{map[string]any{"id": "junk", "name": "Junk Mail", "role": ""}}
	msg := &Message{store: s, Key: "m", AccountID: "acct", Tags: []string{"junk"}}
	if !msg.IsSpam() {
		t.Fatal("precondition: message should resolve as spam")
	}
	msg.SetSpam(false)
	if msg.IsSpam() || len(msg.Tags) != 0 || !msg.FolderDirty {
		t.Fatalf("spam=%v tags=%v dirty=%v", msg.IsSpam(), msg.Tags, msg.FolderDirty)
	}
}

func TestHTMLToRichText(t *testing.T) {
	got := htmlToRichText(`<style>no</style><p>Hello <strong>bold</strong><br><em>it</em></p><ul><li>one</li><li><span style="text-decoration: underline">two</span></li></ul>`)
	want := "Hello **bold**\n*it*\n- one\n- __two__"
	if got != want {
		t.Fatalf("htmlToRichText() = %q, want %q", got, want)
	}
}

func TestHTMLToRichTextPreservesAnchors(t *testing.T) {
	got := htmlToRichText(`<p>Read <a href="https://example.com?a=1&amp;b=2">the docs</a>.</p>`)
	want := "Read [the docs](https://example.com?a=1&b=2)."
	if got != want {
		t.Fatalf("htmlToRichText() = %q, want %q", got, want)
	}
}

func TestTextPlainHTMLDocumentWithNoTextIsEmpty(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: html-empty\r\nContent-Type: text/plain; charset=iso-8859-1\r\n\r\n<html>\r\n<head>\r\n<meta http-equiv=\"Content-Type\" content=\"text/html; charset=iso-8859-1\">\r\n<style type=\"text/css\" style=\"display:none;\"> P {margin-top:0;margin-bottom:0;} </style>\r\n</head>\r\n<body dir=\"ltr\">\r\n<div style=\"font-family: Aptos, Calibri, sans-serif; font-size: 10pt; color: rgb(0, 0, 0);\"><br></div>\r\n</body>\r\n</html>\r\n")
	_, body, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		t.Fatalf("body = %q", body)
	}
}

func TestTextPlainHTMLDocumentWithTextIsConverted(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: html-text\r\nContent-Type: text/plain\r\n\r\n<html><body><p>Hello <strong>world</strong></p></body></html>\r\n")
	_, body, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if body != "Hello **world**" {
		t.Fatalf("body = %q", body)
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

func TestDecodeInvalidUTF8BodyAsWindows1252(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: latin1-missing-charset\r\nContent-Type: text/plain\r\n\r\nUniversit\xe4tsorchester Pr\xe9ludes Besch\xe4ftigten\r\n")
	_, body, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if body != "Universitätsorchester Préludes Beschäftigten" {
		t.Fatalf("body = %q", body)
	}
}

func TestDecodeISO88592Body(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: latin2\r\nContent-Type: text/plain; charset=iso-8859-2\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nUniversit=E4tsorchester Pr=E9ludes Dole=BEel\r\n")
	_, body, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if body != "Universitätsorchester Préludes Doležel" {
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

func TestDecodeMIMEEncodedAddresses(t *testing.T) {
	raw := []byte("From: =?Cp1252?Q?Einstellung_Hilfskr=E4fte?= <no-reply@rrze.uni-erlangen.de>\r\nTo: =?utf-8?Q?Ludwig_Lehnert?= <ludwig.lehnert@fau.de>\r\nSubject: x\r\nDate: Sun, 15 Jun 2026 11:00:48 +0000\r\n\r\nbody\r\n")
	parsed, _, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	msg := messageFromMail("key", parsed, raw, "key.eml.enc", false)
	if msg.From != "Einstellung Hilfskräfte <no-reply@rrze.uni-erlangen.de>" {
		t.Fatalf("from = %q", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "Ludwig Lehnert <ludwig.lehnert@fau.de>" {
		t.Fatalf("to = %#v", msg.To)
	}
}

func TestNormalizeDecodesStoredMIMEEncodedAddresses(t *testing.T) {
	s := &Store{index: emptyIndex()}
	s.index.Messages["m"] = &Message{Key: "m", From: "=?Cp1252?Q?Einstellung_Hilfskr=E4fte?= <no-reply@rrze.uni-erlangen.de>", To: []string{"=?utf-8?Q?Ludwig_Lehnert?= <ludwig.lehnert@fau.de>"}}
	s.normalize()
	msg := s.index.Messages["m"]
	if msg.From != "Einstellung Hilfskräfte <no-reply@rrze.uni-erlangen.de>" {
		t.Fatalf("from = %q", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "Ludwig Lehnert <ludwig.lehnert@fau.de>" {
		t.Fatalf("to = %#v", msg.To)
	}
	if !s.dirty {
		t.Fatal("expected store dirty after address migration")
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

func TestParseRawMultipartSignedExtractsBody(t *testing.T) {
	raw := []byte(signedMultipartMessage(false))
	_, body, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if body != "hello signed" {
		t.Fatalf("body = %q", body)
	}
}

func TestParseRawLegacyEmbeddedMultipartSignedExtractsBody(t *testing.T) {
	raw := []byte(signedMultipartMessage(true))
	_, body, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if body != "hello signed" {
		t.Fatalf("body = %q", body)
	}
}

func signedMultipartMessage(legacyEmbedded bool) string {
	entity := strings.Join([]string{
		`Content-Type: multipart/signed; protocol="application/pgp-signature"; micalg=pgp-sha256; boundary=sig`,
		"",
		"--sig",
		`Content-Type: multipart/mixed; boundary=mix`,
		"",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"hello signed",
		"--mix--",
		"--sig",
		`Content-Type: application/pgp-signature; name="signature.asc"`,
		`Content-Disposition: attachment; filename="signature.asc"`,
		"",
		"signature",
		"--sig--",
		"",
	}, "\r\n")
	headers := strings.Join([]string{
		"From: a@example.com",
		"To: b@example.com",
		"Subject: signed",
		"Date: Tue, 09 Jun 2026 10:00:00 +0000",
	}, "\r\n")
	if legacyEmbedded {
		return headers + "\r\nMIME-Version: 1.0\r\n\r\n" + entity
	}
	return headers + "\r\n" + entity
}

func TestImportSentMarksMessageSent(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	s, err := Open(paths, bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: sent\r\nDate: Tue, 09 Jun 2026 10:00:00 +0000\r\n\r\nbody\r\n")
	msg, err := s.ImportSent("acct", raw)
	if err != nil {
		t.Fatal(err)
	}
	if msg.AccountID != "acct" || !msg.Read || !msg.IsSent() {
		t.Fatalf("sent msg = %#v", msg)
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
		map[string]any{"id": "d", "name": "Gelöscht", "role": ""},
		map[string]any{"id": "e", "name": "Junk Mail", "role": ""},
	}
	msg := &Message{store: s, AccountID: "acct", Tags: []string{"a"}}
	if got := msg.FolderColumn(); got != "in" {
		t.Fatalf("inbox marker = %q", got)
	}
	msg.Tags = []string{"c"}
	if got := msg.FolderColumn(); got != "2026" {
		t.Fatalf("basename marker = %q", got)
	}
	msg.Tags = []string{"d"}
	if got := msg.FolderColumn(); got != "trash" {
		t.Fatalf("deleted marker = %q", got)
	}
	msg.Tags = []string{"e"}
	if got := msg.FolderColumn(); got != "spam" {
		t.Fatalf("junk marker = %q", got)
	}
	if !msg.IsSpam() {
		t.Fatal("junk mailbox should mark message as spam")
	}
}

func TestMessagesClassifyResolvedSentMailboxes(t *testing.T) {
	s := &Store{index: emptyIndex()}
	s.index.Mailboxes["acct"] = []any{
		map[string]any{"id": "sent-name", "name": "Sent Items", "role": ""},
		map[string]any{"id": "sent-role", "name": "Archive Copy", "role": "sent"},
	}

	byName := &Message{Key: "by-name", AccountID: "acct", Tags: []string{"sent-name"}, RawRel: "by-name.eml", store: s}
	s.index.Messages[byName.Key] = byName
	if got := byName.FolderColumn(); got != "out" {
		t.Fatalf("folder column = %q, want out", got)
	}
	if !byName.IsSent() {
		t.Fatal("sent mailbox name should classify message as sent")
	}
	if got := s.Messages(false); len(got) != 0 {
		t.Fatalf("Messages(false) len = %d, want 0", len(got))
	}
	if got := s.MessagesAll(false, true); len(got) != 1 || got[0].Key != byName.Key {
		t.Fatalf("MessagesAll(false, true) = %#v", got)
	}

	byRole := &Message{Key: "by-role", AccountID: "acct", Tags: []string{"sent-role"}, RawRel: "by-role.eml", store: s}
	if !byRole.IsSent() {
		t.Fatal("sent mailbox role should classify message as sent")
	}
}

func TestMessagesExcludeMailboxSpam(t *testing.T) {
	s := &Store{index: emptyIndex()}
	s.index.Mailboxes["acct"] = []any{map[string]any{"id": "junk", "name": "Junk Mail", "role": ""}}
	msg := &Message{Key: "m", AccountID: "acct", Tags: []string{"junk"}, RawRel: "m.eml", store: s}
	s.index.Messages[msg.Key] = msg
	if got := s.Messages(false); len(got) != 0 {
		t.Fatalf("Messages(false) len = %d, want 0", len(got))
	}
	if got := s.Messages(true); len(got) != 1 {
		t.Fatalf("Messages(true) len = %d, want 1", len(got))
	}
}

func TestAggregateReportDMARCImportAndFilter(t *testing.T) {
	s, err := Open(testPaths(t.TempDir()), bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.ImportRaw(reportMail("dmarc.xml", "application/xml", []byte(dmarcXMLReport)))
	if err != nil {
		t.Fatal(err)
	}
	if msg.ReportCategory != "dmarc" || !msg.ReportChecked {
		t.Fatalf("report category = %q checked=%v", msg.ReportCategory, msg.ReportChecked)
	}
	if got := s.Messages(false); len(got) != 0 {
		t.Fatalf("regular inbox len = %d, want 0", len(got))
	}
	if got := s.MessagesCategory("dmarc"); len(got) != 1 {
		t.Fatalf("dmarc category len = %d, want 1", len(got))
	}
	if got := msg.FolderColumn(); got != "dmarc" {
		t.Fatalf("folder column = %q, want dmarc", got)
	}
	report, err := s.AggregateReport(msg)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.Kind != "DMARC" || report.Domain != "example.com" || len(report.Rows) != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestAggregateReportTLSGzipImport(t *testing.T) {
	s, err := Open(testPaths(t.TempDir()), bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.ImportRaw(reportMail("google.com!ludwig-lehnert.de!report.json.gz", "application/gzip", gzipBytes([]byte(tlsJSONReport))))
	if err != nil {
		t.Fatal(err)
	}
	if msg.ReportCategory != "dmarc" {
		t.Fatalf("report category = %q", msg.ReportCategory)
	}
	report, err := s.AggregateReport(msg)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.Kind != "TLSRPT" || report.Organization != "google.com" || report.Domain != "ludwig-lehnert.de" {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Rows) != 2 || report.Rows[0].Count != "12/1" || report.Rows[1].Result != "certificate-host-mismatch" {
		t.Fatalf("report rows = %#v", report.Rows)
	}
}

func reportMail(filename, contentType string, data []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(data)
	return []byte(strings.Join([]string{
		"From: reports@example.com",
		"To: postmaster@example.com",
		"Subject: Report Domain: example.com",
		"Date: Sun, 21 Jun 2026 12:57:39 +0000",
		"Content-Type: multipart/mixed; boundary=abc",
		"",
		"--abc",
		"Content-Type: text/plain",
		"",
		"This is an aggregate report.",
		"--abc",
		"Content-Type: " + contentType,
		"Content-Disposition: attachment; filename=" + filename,
		"Content-Transfer-Encoding: base64",
		"",
		encoded,
		"--abc--",
		"",
	}, "\r\n"))
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, _ = writer.Write(data)
	_ = writer.Close()
	return buf.Bytes()
}

const dmarcXMLReport = `<?xml version="1.0" encoding="UTF-8"?>
<feedback>
  <report_metadata>
    <org_name>Example Reporter</org_name>
    <email>reports@example.net</email>
    <report_id>report-1</report_id>
    <date_range><begin>1782000000</begin><end>1782086400</end></date_range>
  </report_metadata>
  <policy_published>
    <domain>example.com</domain>
    <p>reject</p>
  </policy_published>
  <record>
    <row>
      <source_ip>192.0.2.1</source_ip>
      <count>3</count>
      <policy_evaluated><disposition>none</disposition><dkim>pass</dkim><spf>fail</spf></policy_evaluated>
    </row>
    <identifiers><header_from>example.com</header_from></identifiers>
    <auth_results>
      <dkim><domain>example.com</domain><result>pass</result></dkim>
      <spf><domain>mail.example.com</domain><result>fail</result></spf>
    </auth_results>
  </record>
</feedback>`

const tlsJSONReport = `{
  "organization-name": "google.com",
  "date-range": {"start-datetime": "2026-06-20T00:00:00Z", "end-datetime": "2026-06-21T00:00:00Z"},
  "report-id": "2026.06.20T00.00.00Z+ludwig-lehnert.de@google.com",
  "policies": [{
    "policy": {"policy-type": "sts", "policy-domain": "ludwig-lehnert.de", "mx-host": ["mx.ludwig-lehnert.de"]},
    "summary": {"total-successful-session-count": 12, "total-failure-session-count": 1},
    "failure-details": [{"result-type": "certificate-host-mismatch", "sending-mta-ip": "203.0.113.1", "receiving-mx-hostname": "mx.ludwig-lehnert.de", "failed-session-count": 1}]
  }]
}`

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

func TestImportKnownAddressesMergesIdempotently(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	s, err := Open(paths, bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s.ImportKnownAddresses([]KnownAddress{{Name: "Alice", Email: "Alice <ALICE@example.com>", SeenAt: "2026-06-01T00:00:00Z", Count: 2}})
	s.ImportKnownAddresses([]KnownAddress{{Name: "Ignored", Email: "alice@example.com", SeenAt: "2026-05-01T00:00:00Z", Count: 1}})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	known := s.KnownAddresses()
	if len(known) != 1 {
		t.Fatalf("known = %#v", known)
	}
	if known[0].Email != "alice@example.com" || known[0].Name != "Alice" || known[0].Count != 2 || known[0].SeenAt != "2026-06-01T00:00:00Z" {
		t.Fatalf("known[0] = %#v", known[0])
	}
}
