package ui

import (
	"strings"
	"testing"

	"lehnert.dev/murat/internal/store"
)

func TestTableRowTextCleansTerminalControls(t *testing.T) {
	msg := &store.Message{
		From:       "noreply@expirationwarning.net\r",
		Subject:    "ICANN ERRP f\u00fcr die Domain lehnert.dev\r",
		ReceivedAt: "2026-07-05T01:40:49Z",
		Tags:       []string{"Inbox"},
		Read:       true,
	}

	line, _ := tableRowText(msg, 120)
	if strings.ContainsAny(line, "\r\n\t\x1b") {
		t.Fatalf("line contains terminal control character: %q", line)
	}
	if !strings.Contains(line, "ICANN ERRP f\u00fcr die Domain lehnert.dev") {
		t.Fatalf("line missing subject: %q", line)
	}
}

func TestDrawListClearsEveryRowForEmptyFilter(t *testing.T) {
	app := &App{filter: "unread", keys: DefaultKeys()}
	output := captureStdout(t, func() {
		app.drawList(2, 3, 3, 40)
	})
	if !strings.Contains(output, "no mail; press s to sync") {
		t.Fatalf("empty-state message missing: %q", output)
	}
	for _, cursor := range []string{"\x1b[3;4H", "\x1b[4;4H", "\x1b[5;4H"} {
		if !strings.Contains(output, cursor) {
			t.Fatalf("row %q was not redrawn: %q", cursor, output)
		}
	}
}

func TestDrawListClearsEveryRowForEmptySearch(t *testing.T) {
	app := &App{filter: "search", keys: DefaultKeys()}
	output := captureStdout(t, func() {
		app.drawList(4, 1, 3, 30)
	})
	if strings.Contains(output, "no mail") {
		t.Fatalf("search empty state unexpectedly showed sync prompt: %q", output)
	}
	for _, cursor := range []string{"\x1b[5;2H", "\x1b[6;2H", "\x1b[7;2H"} {
		if !strings.Contains(output, cursor) {
			t.Fatalf("row %q was not cleared: %q", cursor, output)
		}
	}
}
