package lsp

import (
	"testing"

	"lehnert.dev/murat/internal/store"
)

func TestCompleteAddressesHeaders(t *testing.T) {
	addresses := completionItems([]store.KnownAddress{{Name: "Alice", Email: "alice@example.com"}})
	items := completeAddresses("To: Al\nSubject: Al", 0, 6, addresses)
	if len(items) != 1 {
		t.Fatalf("completion count = %d", len(items))
	}
	if items[0]["label"] != "Alice <alice@example.com>" {
		t.Fatalf("label = %v", items[0]["label"])
	}
	if got := completeAddresses("From: Al\nSubject: Al", 0, 8, addresses); len(got) != 1 {
		t.Fatalf("from completion count = %d", len(got))
	}
	if got := completeAddresses("To: Al\nSubject: Al", 1, 11, addresses); len(got) != 0 {
		t.Fatalf("subject completion count = %d", len(got))
	}
}
