package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/protocol"
	"lehnert.dev/murat/internal/store"
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

func TestMessageWithinDays(t *testing.T) {
	now := time.Now().UTC()
	if !messageWithinDays(&store.Message{ReceivedAt: now.Format(time.RFC3339)}, 0) {
		t.Fatal("expected message today")
	}
	if messageWithinDays(&store.Message{ReceivedAt: now.AddDate(0, 0, -1).Format(time.RFC3339)}, 0) {
		t.Fatal("unexpected message before today")
	}
	if !messageWithinDays(&store.Message{ReceivedAt: now.AddDate(0, 0, -7).Format(time.RFC3339)}, 7) {
		t.Fatal("expected message within 7 days")
	}
	if messageWithinDays(&store.Message{ReceivedAt: now.AddDate(0, 0, -8).Format(time.RFC3339)}, 7) {
		t.Fatal("unexpected message older than 7 days")
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
