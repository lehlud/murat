package protocol

import (
	"reflect"
	"testing"
)

func TestFilterUnknownIMAPUIDsInitialLimit(t *testing.T) {
	got := filterUnknownIMAPUIDs([]string{"1", "2", "3", "4", "5"}, "INBOX", nil, 2)
	want := []string{"4", "5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterUnknownIMAPUIDs() = %v, want %v", got, want)
	}
}

func TestFilterUnknownIMAPUIDsDeltaOnly(t *testing.T) {
	known := map[string]bool{"imap:INBOX:10": true, "imap:INBOX:12": true}
	got := filterUnknownIMAPUIDs([]string{"1", "11", "12", "13", "14"}, "INBOX", known, 0)
	want := []string{"13", "14"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterUnknownIMAPUIDs() = %v, want %v", got, want)
	}
}

func TestIMAPSyncMailboxesAddsSent(t *testing.T) {
	got := imapSyncMailboxes("INBOX", "Sent")
	want := []string{"INBOX", "Sent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imapSyncMailboxes() = %v, want %v", got, want)
	}
}

func TestIMAPSyncMailboxesDedupesSent(t *testing.T) {
	got := imapSyncMailboxes("Sent", "sent")
	want := []string{"Sent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imapSyncMailboxes() = %v, want %v", got, want)
	}
}

func TestParseIMAPListMailboxDetectsSentFlag(t *testing.T) {
	box, ok := parseIMAPListMailbox(`* LIST (\HasNoChildren \Sent) "/" "Gesendet"`)
	if !ok {
		t.Fatal("mailbox not parsed")
	}
	if box.name != "Gesendet" || !box.sent {
		t.Fatalf("mailbox = %#v", box)
	}
}

func TestParseIMAPListMailboxDetectsSentName(t *testing.T) {
	box, ok := parseIMAPListMailbox(`* LIST (\HasNoChildren) "/" "[Gmail]/Sent Mail"`)
	if !ok {
		t.Fatal("mailbox not parsed")
	}
	if box.name != "[Gmail]/Sent Mail" || !box.sent {
		t.Fatalf("mailbox = %#v", box)
	}
}

func TestSentIMAPMailboxPrefersFirstSent(t *testing.T) {
	got := sentIMAPMailbox([]imapMailboxInfo{{name: "Archive"}, {name: "Sent", sent: true}})
	if got != "Sent" {
		t.Fatalf("sentIMAPMailbox() = %q, want Sent", got)
	}
}
