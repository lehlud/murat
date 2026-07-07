package protocol

import (
	"io"
	"net/mail"
	"strings"
	"testing"

	"lehnert.dev/murat/internal/store"
)

func TestJMAPEmailToEMLSanitizesHeaderValues(t *testing.T) {
	item := map[string]any{
		"id":         "mail1\r\nX-Bad-ID: yes",
		"receivedAt": "2026-07-05T01:40:49Z\r\nX-Bad-Date: yes",
		"subject":    "ICANN ERRP f\u00fcr die Domain lehnert.dev\r\nX-Injected: yes",
		"from":       []any{map[string]any{"email": "alice@example.com", "name": "Alice\r\nX-Bad-From: yes"}},
		"to":         []any{map[string]any{"email": "bob@example.com", "name": "Bob"}},
		"bodyValues": map[string]any{"body": map[string]any{"value": "hello\r\nworld"}},
		"textBody":   []any{map[string]any{"partId": "body", "type": "text/plain"}},
	}

	raw, err := jmapEmailToEML(store.Account{}, nil, "acct", item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "\r\nX-Injected:") || strings.Contains(raw, "\r\nX-Bad-ID:") || strings.Contains(raw, "\r\nX-Bad-Date:") || strings.Contains(raw, "\r\nX-Bad-From:") {
		t.Fatalf("raw contains injected header:\n%s", raw)
	}

	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Header.Get("Subject"); got != "ICANN ERRP f\u00fcr die Domain lehnert.dev X-Injected: yes" {
		t.Fatalf("subject = %q", got)
	}
	if parsed.Header.Get("X-Injected") != "" || parsed.Header.Get("X-Bad-ID") != "" || parsed.Header.Get("X-Bad-Date") != "" || parsed.Header.Get("X-Bad-From") != "" {
		t.Fatalf("unexpected injected headers: %#v", parsed.Header)
	}
	body, err := io.ReadAll(parsed.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hello\r\nworld") {
		t.Fatalf("body was unexpectedly changed: %q", string(body))
	}
}

func TestSMTPMessageHeadersSanitizeControlCharacters(t *testing.T) {
	headers := messageHeaders(store.Account{Email: "account@example.com"}, Draft{
		From:    "Alice <alice@example.com>\r\nX-Bad-From: yes",
		To:      "bob@example.com\r\nX-Bad-To: yes",
		Cc:      "carol@example.com",
		Subject: "hello\r\nBcc: victim@example.com",
	})
	joined := strings.Join(headers, "\r\n")
	if strings.Contains(joined, "\r\nX-Bad-From:") || strings.Contains(joined, "\r\nX-Bad-To:") || strings.Contains(joined, "\r\nBcc:") {
		t.Fatalf("headers contain injected header:\n%s", joined)
	}
	for _, header := range headers {
		if strings.ContainsAny(header, "\r\n\t\x1b") {
			t.Fatalf("header contains control character: %q", header)
		}
	}
	if !strings.Contains(joined, "Subject: hello Bcc: victim@example.com") {
		t.Fatalf("subject not cleaned into one header: %q", joined)
	}
}

func TestSMTPEnvelopeValuesSanitizeControlCharacters(t *testing.T) {
	draft := Draft{
		From: "alice@example.com\r\nX-Bad-From: yes",
		To:   "bob@example.com\r\nX-Bad-To: yes, carol@example.com\t",
	}
	if got := draftFromEmail(store.Account{}, draft); strings.ContainsAny(got, "\r\n\t\x1b") {
		t.Fatalf("from contains control character: %q", got)
	}
	for _, rcpt := range recipients(draft) {
		if strings.ContainsAny(rcpt, "\r\n\t\x1b") {
			t.Fatalf("recipient contains control character: %q", rcpt)
		}
	}
}
