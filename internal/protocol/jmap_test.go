package protocol

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lehnert.dev/murat/internal/store"
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

func TestJMAPFetchIDsIncludesKnownMissingAttachments(t *testing.T) {
	s, err := store.Open(testStorePaths(t.TempDir()), bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.ImportRaw([]byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: x\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	msg.SetRemote("acct", "jmap:a")
	known := s.KnownRemoteIDs("acct")

	got := jmapFetchIDs([]string{"a", "b"}, "acct", s, known, 0)
	want := []string{"b", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("jmapFetchIDs() = %v, want %v", got, want)
	}
}

func TestHTTPJSONRetriesRejectedBearerAsBasic(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			if got := r.Header.Get("Authorization"); got != "Bearer password" {
				t.Fatalf("first authorization = %q", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("me@example.com:password"))
		if got := r.Header.Get("Authorization"); got != want {
			t.Fatalf("fallback authorization = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(`{"apiUrl":"https://example.test/jmap"}`))
	}))
	defer server.Close()

	var session jmapSession
	if err := httpJSON(store.Account{Email: "me@example.com", Secret: "password"}, "GET", server.URL, nil, &session); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || session.APIURL == "" {
		t.Fatalf("requests = %d, session = %#v", requests, session)
	}
}

func TestJMAPEmailToEMLIncludesAttachments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer token"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
		if got := r.URL.Path; got != "/download/acct/blob/report.pdf" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("accept"); got != "application/pdf" {
			t.Fatalf("accept = %q", got)
		}
		_, _ = w.Write([]byte("pdf data"))
	}))
	defer server.Close()

	item := map[string]any{
		"id":         "mail1",
		"receivedAt": "2026-06-19T12:08:08Z",
		"subject":    "with attachment",
		"from":       []any{map[string]any{"email": "a@example.com", "name": "Alice"}},
		"to":         []any{map[string]any{"email": "b@example.com", "name": "Bob"}},
		"bodyValues": map[string]any{"body": map[string]any{"value": "hello"}},
		"textBody":   []any{map[string]any{"partId": "body", "type": "text/plain"}},
		"attachments": []any{map[string]any{
			"blobId": "blob",
			"name":   "report.pdf",
			"type":   "application/pdf",
		}},
	}
	session := &jmapSession{DownloadURL: server.URL + "/download/{accountId}/{blobId}/{name}?accept={type}"}
	raw, err := jmapEmailToEML(store.Account{Secret: "token"}, session, "acct", item)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Content-Type: multipart/mixed; boundary=",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Type: application/pdf; name=\"report.pdf\"",
		"Content-Disposition: attachment; filename=\"report.pdf\"",
		"cGRmIGRhdGE=",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw missing %q:\n%s", want, raw)
		}
	}
}

func TestJMAPMailboxRoles(t *testing.T) {
	roles := jmapMailboxRoles([]map[string]any{
		{"id": "in", "name": "Inbox", "role": "inbox"},
		{"id": "sent", "name": "Sent Mail", "role": ""},
		{"id": "junk", "name": "Junk Mail", "role": ""},
		{"id": "trash", "name": "Deleted Items", "role": ""},
	})
	if roles.inbox != "in" || roles.sent != "sent" || roles.spam != "junk" || roles.trash != "trash" {
		t.Fatalf("roles = %#v", roles)
	}
}

func TestJMAPIDFromRemoteID(t *testing.T) {
	if got := jmapIDFromRemoteID("jmap:abc"); got != "abc" {
		t.Fatalf("jmapIDFromRemoteID() = %q", got)
	}
	if got := jmapIDFromRemoteID("imap:INBOX:1"); got != "" {
		t.Fatalf("non-jmap id = %q", got)
	}
}

func TestJMAPPatchPathEscapesMailboxID(t *testing.T) {
	if got := jmapPatchPath("mailboxIds", "a/b~c"); got != "mailboxIds/a~1b~0c" {
		t.Fatalf("jmapPatchPath() = %q", got)
	}
}

func testStorePaths(dir string) store.Paths {
	return store.Paths{
		ConfigDir:    filepath.Join(dir, "config"),
		ConfigFile:   filepath.Join(dir, "config", "config.toml"),
		DataDir:      filepath.Join(dir, "data"),
		KeyFile:      filepath.Join(dir, "data", "key.ssh.json"),
		IndexFile:    filepath.Join(dir, "data", "mail.enc.json"),
		AccountsFile: filepath.Join(dir, "data", "accounts.enc.json"),
		SearchFile:   filepath.Join(dir, "data", "search.enc.json"),
		BodyDir:      filepath.Join(dir, "data", "eml"),
		RawDir:       filepath.Join(dir, "data", "eml"),
	}
}
