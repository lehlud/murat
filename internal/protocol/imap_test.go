package protocol

import (
	"encoding/base64"
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
	got := imapSyncMailboxes("INBOX", "Sent", "Junk Mail", "Trash")
	want := []string{"INBOX", "Sent", "Junk Mail", "Trash"}
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

func TestParseIMAPListMailboxDetectsSpamAndTrash(t *testing.T) {
	spam, ok := parseIMAPListMailbox(`* LIST (\HasNoChildren \Junk) "/" "Junk Mail"`)
	if !ok || !spam.spam {
		t.Fatalf("spam mailbox = %#v ok=%v", spam, ok)
	}
	trash, ok := parseIMAPListMailbox(`* LIST (\HasNoChildren) "/" "Deleted Items"`)
	if !ok || !trash.trash {
		t.Fatalf("trash mailbox = %#v ok=%v", trash, ok)
	}
}

func TestIMAPFoldersUsesSpecialMailboxes(t *testing.T) {
	folders := imapFolders("INBOX", []imapMailboxInfo{{name: "Sent", sent: true}, {name: "Junk", spam: true}, {name: "Trash", trash: true}})
	if folders.primary != "INBOX" || folders.sent != "Sent" || folders.spam != "Junk" || folders.trash != "Trash" {
		t.Fatalf("folders = %#v", folders)
	}
}

func TestParseIMAPFetchFlags(t *testing.T) {
	uid, flags, ok := parseIMAPFetchFlags(`* 1 FETCH (FLAGS (\Seen \Answered) UID 42)`)
	if !ok || uid != "42" || !imapFlagsSeen(flags) {
		t.Fatalf("uid=%q flags=%q ok=%v", uid, flags, ok)
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

func TestXOAUTH2InitialResponse(t *testing.T) {
	got, err := base64.StdEncoding.DecodeString(xoauth2InitialResponse("user@example.com", "access"))
	if err != nil {
		t.Fatal(err)
	}
	want := "user=user@example.com\x01auth=Bearer access\x01\x01"
	if string(got) != want {
		t.Fatalf("xoauth2 response = %q", string(got))
	}
}
