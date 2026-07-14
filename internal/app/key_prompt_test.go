package app

import (
	"errors"
	"testing"

	"lehnert.dev/murat/internal/store"
)

func TestLoadLocalStoreKeyReturnsOtherErrors(t *testing.T) {
	original := loadStoreKey
	t.Cleanup(func() { loadStoreKey = original })
	want := errors.New("key missing")
	loadStoreKey = func(store.Paths) ([]byte, error) { return nil, want }
	_, err := loadLocalStoreKey(store.Paths{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
