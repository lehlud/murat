package ui

import (
	"strings"
	"testing"

	"lehnert.dev/murat/internal/config"
)

func TestDefaultThemeUsesLocalPalette(t *testing.T) {
	theme := DefaultTheme()
	for name, value := range map[string]string{
		"header":   theme.Header,
		"selected": theme.Selected,
		"status":   theme.Status,
		"link":     theme.Link,
	} {
		if !strings.Contains(value, "38;2;") && !strings.Contains(value, "48;2;") {
			t.Fatalf("%s style is not truecolor: %q", name, value)
		}
	}
	if !strings.Contains(theme.Header, "48;2;96;165;250") {
		t.Fatalf("header missing kitty blue bg: %q", theme.Header)
	}
	if !strings.Contains(theme.Selected, "48;2;31;111;235") {
		t.Fatalf("selected missing selection bg: %q", theme.Selected)
	}
}

func TestThemeFromConfig(t *testing.T) {
	theme := ThemeFromConfig(config.ThemeConfig{Header: "reverse", Label: "dim", Selected: "bold reverse", Error: "bright-red"})
	if theme.Header != "\x1b[7m" {
		t.Fatalf("header style = %q", theme.Header)
	}
	if theme.Label != "\x1b[2m" {
		t.Fatalf("label style = %q", theme.Label)
	}
	if theme.Selected != "\x1b[1;7m" {
		t.Fatalf("selected style = %q", theme.Selected)
	}
	if theme.Error != "\x1b[91m" {
		t.Fatalf("error style = %q", theme.Error)
	}
	if theme.Status == "" {
		t.Fatal("missing default status style")
	}
}

func TestThemeFromConfigSupportsHex(t *testing.T) {
	theme := ThemeFromConfig(config.ThemeConfig{Header: "bold fg=#05070a bg=#60a5fa"})
	if !strings.Contains(theme.Header, "38;2;5;7;10") || !strings.Contains(theme.Header, "48;2;96;165;250") {
		t.Fatalf("header style = %q", theme.Header)
	}
}

func TestKeysFromConfig(t *testing.T) {
	keys := KeysFromConfig(config.KeysConfig{Quit: "x", Open: "ENTER", ForceQuit: "Q"})
	if keys.Quit != "x" || keys.Open != "enter" || keys.ForceQuit != "Q" {
		t.Fatalf("keys = %#v", keys)
	}
	if keys.Sync != "s" {
		t.Fatalf("default sync key = %q", keys.Sync)
	}
}
