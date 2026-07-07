package store

import "testing"

func TestDecodeHeaderCleansControlCharacters(t *testing.T) {
	got := decodeHeader("ICANN ERRP f\u00fcr die Domain lehnert.dev\r\nX-Bad: yes\tmore\x1b")
	want := "ICANN ERRP f\u00fcr die Domain lehnert.dev X-Bad: yes more"
	if got != want {
		t.Fatalf("decodeHeader() = %q, want %q", got, want)
	}
}

func TestNormalizeCleansStoredMetadataControlCharacters(t *testing.T) {
	s := &Store{index: emptyIndex()}
	s.index.Messages["m"] = &Message{
		Key:     "m",
		From:    "Alice\r <alice@example.com>",
		To:      []string{"Bob\n <bob@example.com>"},
		Cc:      []string{"Carol\t <carol@example.com>"},
		Subject: "ICANN ERRP f\u00fcr die Domain lehnert.dev\r",
	}

	s.normalize()

	msg := s.index.Messages["m"]
	if msg.Subject != "ICANN ERRP f\u00fcr die Domain lehnert.dev" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if msg.From != "Alice <alice@example.com>" {
		t.Fatalf("from = %q", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "Bob <bob@example.com>" {
		t.Fatalf("to = %#v", msg.To)
	}
	if len(msg.Cc) != 1 || msg.Cc[0] != "Carol <carol@example.com>" {
		t.Fatalf("cc = %#v", msg.Cc)
	}
	if !s.dirty {
		t.Fatal("expected store dirty after metadata cleanup")
	}
}
