package protocol

import (
	"strings"
	"testing"

	"lehnert.dev/murat/internal/oauth"
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
	msg := Message(store.Account{Email: "a@example.com"}, draft)
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
	msg := Message(store.Account{Email: "fallback@example.com"}, Draft{From: "Alice <alice@example.com>", To: "bob@example.com", Body: "body"})
	if !strings.Contains(msg, "From: Alice <alice@example.com>") {
		t.Fatalf("message = %q", msg)
	}
}

func TestMessageUsesRawMIME(t *testing.T) {
	msg := Message(store.Account{Email: "a@example.com"}, Draft{To: "b@example.com", RawMIME: "Content-Type: multipart/encrypted\n\nbody"})
	for _, want := range []string{"From: a@example.com", "MIME-Version: 1.0", "Content-Type: multipart/encrypted\r\n\r\nbody"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestXOAUTH2SMTPAuth(t *testing.T) {
	mechanism, response, err := (xoauth2SMTPAuth{username: "user@example.com", accessToken: "access"}).Start(nil)
	if err != nil {
		t.Fatal(err)
	}
	if mechanism != "XOAUTH2" {
		t.Fatalf("mechanism = %q", mechanism)
	}
	want := "user=user@example.com\x01auth=Bearer access\x01\x01"
	if string(response) != want {
		t.Fatalf("response = %q", string(response))
	}
}

func TestSMTPOAuthScopesAddsSend(t *testing.T) {
	scopes := smtpOAuthScopes([]string{oauth.ScopeMicrosoftIMAP})
	if !hasScope(scopes, oauth.ScopeMicrosoftIMAP) || !hasScope(scopes, oauth.ScopeMicrosoftSMTP) || !hasScope(scopes, oauth.ScopeOfflineAccess) {
		t.Fatalf("scopes = %#v", scopes)
	}
}
