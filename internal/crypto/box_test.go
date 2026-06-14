package crypto

import "testing"

func TestBoxRoundTrip(t *testing.T) {
	box, err := NewBox([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != "hello" {
		t.Fatalf("got %q", opened)
	}
}
