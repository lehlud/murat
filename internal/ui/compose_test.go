package ui

import (
	"strings"
	"testing"

	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/protocol"
)

func TestComposePGPLineHidesOptionsOutsideMenu(t *testing.T) {
	line := composePGPLine(protocol.Draft{}, pgp.Availability{Sign: true, AttachPublicKey: true}, false)
	if strings.Contains(line, "sign=") || strings.Contains(line, "pubkey=") || strings.Contains(line, "encrypt=") || strings.Contains(line, "self=") {
		t.Fatalf("line shows menu options outside menu: %q", line)
	}
}

func TestComposePGPMenuHidesUnavailableOptions(t *testing.T) {
	line := composePGPLine(protocol.Draft{}, pgp.Availability{Sign: true, AttachPublicKey: true}, true)
	if !strings.Contains(line, "sign=") || !strings.Contains(line, "pubkey=") {
		t.Fatalf("line missing available options: %q", line)
	}
	if strings.Contains(line, "encrypt=") || strings.Contains(line, "self=") {
		t.Fatalf("line shows unavailable options: %q", line)
	}
}

func TestToggleSelfEncryptEnablesEncrypt(t *testing.T) {
	draft := protocol.Draft{}
	togglePGP(&draft, "self-encrypt")
	options := pgpSet(draft.PGP)
	if !options["encrypt"] || !options["self-encrypt"] {
		t.Fatalf("PGP options = %q", draft.PGP)
	}
	togglePGP(&draft, "encrypt")
	options = pgpSet(draft.PGP)
	if options["encrypt"] || options["self-encrypt"] {
		t.Fatalf("PGP options after disabling encrypt = %q", draft.PGP)
	}
}
