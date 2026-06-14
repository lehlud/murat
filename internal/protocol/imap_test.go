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
