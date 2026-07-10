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

func TestSecretBufferHelpers(t *testing.T) {
	value, chars := removeLastSecretRune([]byte("abc"), 3)
	if string(value) != "ab" || chars != 2 {
		t.Fatalf("removeLastSecretRune = %q, %d", value, chars)
	}
	clearSecretBytes(value)
	for _, b := range value {
		if b != 0 {
			t.Fatalf("secret byte not cleared: %#v", value)
		}
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

func TestBareURLLinks(t *testing.T) {
	text := "see https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwesen-umsetzen-1279612 now"
	links := markdownLinks(text)
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	url := "https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwesen-umsetzen-1279612"
	if links[0].start != 4 || links[0].url != url {
		t.Fatalf("link = %#v", links[0])
	}
}

func TestWrapUnwrapsBrokenBareURL(t *testing.T) {
	url := "https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwesen-umsetzen-1279612"
	broken := "https://www.din.de/de/din-und-seine-partner/termine/digitalisierung-im-bauwe\nsen-umsetzen-1279612"
	lines := wrap(joinWrappedBareURLs(broken), 40)
	if strings.Contains(strings.Join(lines, "\n"), "bauwe\nsen") {
		t.Fatalf("url remained broken: %#v", lines)
	}
	for _, line := range lines {
		links := markdownLinks(line)
		if len(links) == 0 {
			continue
		}
		if links[0].url != url {
			t.Fatalf("url = %q, want %q", links[0].url, url)
		}
		return
	}
	t.Fatalf("wrapped lines contain no resolvable link: %#v", lines)
}

func TestRichPlainTextShowsLinkLabelOnly(t *testing.T) {
	got := richPlainText("Read [the docs](https://example.com) now")
	want := "Read the docs now"
	if got != want {
		t.Fatalf("richPlainText() = %q, want %q", got, want)
	}
}

func TestWrapKeepsLongMarkdownLinkResolvable(t *testing.T) {
	url := "https://manage.kmail-lists.com/subscriptions/unsubscribe?a=" + strings.Repeat("x", 96)
	lines := wrap("No longer want [Unsubscribe]("+url+") now", 20)
	for _, line := range lines {
		links := markdownLinks(line)
		if len(links) == 0 {
			continue
		}
		if links[0].url != url {
			t.Fatalf("url = %q, want %q", links[0].url, url)
		}
		if got := richPlainText(line); !strings.Contains(got, "Unsubscribe") {
			t.Fatalf("plain wrapped line = %q", got)
		}
		return
	}
	t.Fatalf("wrapped lines contain no resolvable link: %#v", lines)
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

func TestDraftAttachmentsFromDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	attachments, err := draftAttachmentsFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments len = %d", len(attachments))
	}
	if attachments[0].Filename != "a.txt" || string(attachments[0].Data) != "a" {
		t.Fatalf("attachment[0] = %#v", attachments[0])
	}
	if attachments[1].Filename != "b.txt" || string(attachments[1].Data) != "b" {
		t.Fatalf("attachment[1] = %#v", attachments[1])
	}
}

func TestDraftAttachmentsFromDirectoryRequiresFiles(t *testing.T) {
	if _, err := draftAttachmentsFromDirectory(t.TempDir()); err == nil {
		t.Fatal("expected empty directory error")
	}
}
