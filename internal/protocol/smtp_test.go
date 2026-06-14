package protocol

import (
	"strings"
	"testing"

	"lehnert.dev/murat/internal/store"
)

func TestMultipartMessageIncludesAttachment(t *testing.T) {
	draft := Draft{
		To:      "b@example.com",
		Subject: "key",
		Body:    "body",
		Attachments: []Attachment{{
			Filename:    "a@example.com.asc",
			ContentType: "application/pgp-keys",
			Data:        []byte("public key"),
		}},
	}
	msg := message(store.Account{Email: "a@example.com"}, draft)
	for _, want := range []string{
		"Content-Type: multipart/mixed; boundary=",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Type: application/pgp-keys; name=\"a@example.com.asc\"",
		"Content-Disposition: attachment; filename=\"a@example.com.asc\"",
		"Content-Transfer-Encoding: base64",
		"cHVibGljIGtleQ==",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestMessageUsesDraftFrom(t *testing.T) {
	msg := message(store.Account{Email: "fallback@example.com"}, Draft{From: "Alice <alice@example.com>", To: "bob@example.com", Body: "body"})
	if !strings.Contains(msg, "From: Alice <alice@example.com>") {
		t.Fatalf("message = %q", msg)
	}
}
