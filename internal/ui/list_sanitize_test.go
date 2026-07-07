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
