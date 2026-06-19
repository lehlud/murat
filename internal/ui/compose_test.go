package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/protocol"
)

func TestComposePGPLineHidesOptionsOutsideMenu(t *testing.T) {
	line := composePGPLine(protocol.Draft{}, pgp.Availability{Sign: true, AttachPublicKey: true}, false)
	if strings.Contains(line, "sign=") || strings.Contains(line, "pubkey=") || strings.Contains(line, "encrypt=") || strings.Contains(line, "self=") {
		t.Fatalf("line shows menu options outside menu: %q", line)
	}
}

func TestComposePGPMenuHidesUnavailableOptions(t *testing.T) {
	line := composePGPLine(protocol.Draft{}, pgp.Availability{Sign: true, AttachPublicKey: true}, true)
	if !strings.Contains(line, "sign=") || !strings.Contains(line, "pubkey=") {
		t.Fatalf("line missing available options: %q", line)
	}
	if strings.Contains(line, "encrypt=") || strings.Contains(line, "self=") {
		t.Fatalf("line shows unavailable options: %q", line)
	}
}

func TestToggleSelfEncryptEnablesEncrypt(t *testing.T) {
	draft := protocol.Draft{}
	togglePGP(&draft, "self-encrypt")
	options := pgpSet(draft.PGP)
	if !options["encrypt"] || !options["self-encrypt"] {
		t.Fatalf("PGP options = %q", draft.PGP)
	}
	togglePGP(&draft, "encrypt")
	options = pgpSet(draft.PGP)
	if options["encrypt"] || options["self-encrypt"] {
		t.Fatalf("PGP options after disabling encrypt = %q", draft.PGP)
	}
}

func TestPGPBodyProtectionEnabled(t *testing.T) {
	for _, value := range []string{"encrypt", "sign", "self-encrypt"} {
		if !pgpBodyProtectionEnabled(value) {
			t.Fatalf("pgpBodyProtectionEnabled(%q) = false", value)
		}
	}
	if pgpBodyProtectionEnabled("attach-pubkey") {
		t.Fatal("attach-pubkey should not count as body protection")
	}
}

func TestMarkdownLinks(t *testing.T) {
	links := markdownLinks("Read [the docs](https://example.com) now")
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].start != 5 || links[0].end != 13 || links[0].url != "https://example.com" {
		t.Fatalf("link = %#v", links[0])
	}
}

func TestRichPlainTextShowsLinkLabelOnly(t *testing.T) {
	got := richPlainText("Read [the docs](https://example.com) now")
	want := "Read the docs now"
	if got != want {
		t.Fatalf("richPlainText() = %q, want %q", got, want)
	}
}

func TestFormatDraftPreviewShowsAttachments(t *testing.T) {
	preview := formatDraftPreview(protocol.Draft{
		From:    "alice@example.com",
		To:      "bob@example.com",
		Subject: "hi",
		Body:    "body",
		Attachments: []protocol.Attachment{{
			Filename:    "notes.txt",
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	})
	for _, want := range []string{"Attachments:", "  - notes.txt (text/plain, 5B)", "\n\nbody"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
}

func TestDraftAttachmentFromPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachment, err := draftAttachmentFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Filename != "notes.txt" || string(attachment.Data) != "hello" {
		t.Fatalf("attachment = %#v", attachment)
	}
	if !strings.HasPrefix(attachment.ContentType, "text/plain") {
		t.Fatalf("content type = %q", attachment.ContentType)
	}
}
