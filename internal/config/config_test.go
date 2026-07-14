package config

import "testing"

func TestPGPOptionsFromConfig(t *testing.T) {
	cfg, err := parse(`
[ui]
split = "vertical"
page_size = 50
editor = "hx"

[theme]
header = "reverse"
label = "dim"
selected = "bold reverse"

[keys]
quit = "q"
force_quit = "Q"

[crypto]
gpg_recipient = "abc"

[pgp]
enabled = true
sign = true
encrypt = false
encrypt_to_self = true
attach_public_key = true
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PGPOptions(); got != "sign,attach-pubkey" {
		t.Fatalf("PGPOptions() = %q", got)
	}
	if cfg.UI.Split != "vertical" || cfg.UI.PageSize != 50 || cfg.UI.Editor != "hx" {
		t.Fatalf("UI config = %#v", cfg.UI)
	}
	if cfg.Theme.Header != "reverse" || cfg.Theme.Label != "dim" || cfg.Theme.Selected != "bold reverse" {
		t.Fatalf("theme config = %#v", cfg.Theme)
	}
	if cfg.Keys.Quit != "q" || cfg.Keys.ForceQuit != "Q" {
		t.Fatalf("keys config = %#v", cfg.Keys)
	}
	if cfg.PGP.Identity != "abc" {
		t.Fatalf("crypto config = %#v", cfg.PGP)
	}
}

func TestPGPOptionsDisabled(t *testing.T) {
	cfg, err := parse(`
[pgp]
enabled = false
sign = true
attach_public_key = true
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PGPOptions(); got != "" {
		t.Fatalf("PGPOptions() = %q", got)
	}
}

func TestStripCommentPreservesQuotedHash(t *testing.T) {
	got := stripComment(`public_key = "abc#def" # comment`)
	if got != `public_key = "abc#def" ` {
		t.Fatalf("stripComment() = %q", got)
	}
}
