package store

import (
	"bytes"
	"strings"
	"testing"
)

func TestOpenBodyKeepsPlainAlternativeAndOrdersCIDImages(t *testing.T) {
	s, err := Open(testPaths(t.TempDir()), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(strings.Join([]string{
		"From: a@example.com",
		"To: b@example.com",
		"Subject: inline body",
		"Content-Type: multipart/related; boundary=rel",
		"",
		"--rel",
		"Content-Type: multipart/alternative; boundary=alt",
		"",
		"--alt",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"plain version",
		"--alt",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>before</p><img src=\"CID:Logo%40Example\" alt=\"brand\"><p>after <img src=\"cid:missing\" alt=\"missing\"></p>",
		"--alt--",
		"--rel",
		"Content-Type: image/png",
		"Content-Disposition: inline",
		"Content-ID: <logo@example>",
		"Content-Transfer-Encoding: base64",
		"",
		"AQID",
		"--rel--",
		"",
	}, "\r\n"))
	msg, err := s.ImportRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	body, err := s.OpenBody(msg)
	if err != nil {
		t.Fatal(err)
	}
	if body.Text != "plain version" {
		t.Fatalf("body text = %q", body.Text)
	}
	if len(body.Parts) != 3 {
		t.Fatalf("parts = %#v", body.Parts)
	}
	if !strings.Contains(body.Parts[0].Text, "before") {
		t.Fatalf("leading text = %q", body.Parts[0].Text)
	}
	image := body.Parts[1].Image
	if image == nil || image.ContentID != "logo@example" || image.Alt != "brand" || !bytes.Equal(image.Data, []byte{1, 2, 3}) {
		t.Fatalf("image = %#v", image)
	}
	if !strings.Contains(body.Parts[2].Text, "after") || !strings.Contains(body.Parts[2].Text, "[image: missing]") {
		t.Fatalf("trailing text = %q", body.Parts[2].Text)
	}
	attachments, err := s.Attachments(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || !attachments[0].Inline || attachments[0].ContentID != "logo@example" {
		t.Fatalf("attachments = %#v", attachments)
	}
}

func TestUnreferencedCIDImageDoesNotReplacePlainPreview(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"From: a@example.com",
		"Content-Type: multipart/related; boundary=rel",
		"",
		"--rel",
		"Content-Type: multipart/alternative; boundary=alt",
		"",
		"--alt",
		"Content-Type: text/plain",
		"",
		"plain version",
		"--alt",
		"Content-Type: text/html",
		"",
		"<p>html version</p>",
		"--alt--",
		"--rel",
		"Content-Type: image/png",
		"Content-Disposition: inline",
		"Content-ID: <unused>",
		"",
		"not-an-image",
		"--rel--",
		"",
	}, "\r\n"))
	_, text, _, err := parseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := inlineBodyParts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if text != "plain version" || len(parts) != 0 {
		t.Fatalf("text = %q, parts = %#v", text, parts)
	}
}
