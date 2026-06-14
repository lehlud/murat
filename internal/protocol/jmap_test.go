package protocol

import (
	"reflect"
	"testing"
)

func TestFilterUnknownJMAPIDs(t *testing.T) {
	known := map[string]bool{"jmap:b": true}
	got := filterUnknownJMAPIDs([]string{"a", "b", "c"}, known, 0)
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterUnknownJMAPIDs() = %v, want %v", got, want)
	}
}

func TestFilterUnknownJMAPIDsLimit(t *testing.T) {
	got := filterUnknownJMAPIDs([]string{"a", "b", "c"}, nil, 2)
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterUnknownJMAPIDs() = %v, want %v", got, want)
	}
}
