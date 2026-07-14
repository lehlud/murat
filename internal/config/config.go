package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	UI    UIConfig
	Theme ThemeConfig
	Keys  KeysConfig
	PGP   PGPConfig
}

type UIConfig struct {
	Split    string
	PageSize int
	Editor   string
}

type ThemeConfig struct {
	Header   string
	Label    string
	Selected string
	Unread   string
	Status   string
	Error    string
	Divider  string
	Tag      string
	Spam     string
	Dim      string
	Link     string
}

type KeysConfig struct {
	Quit         string
	ForceQuit    string
	Sync         string
	Compose      string
	Filter       string
	Actions      string
	Open         string
	Next         string
	Prev         string
	Down         string
	Up           string
	PageDown     string
	PageUp       string
	Top          string
	Bottom       string
	CycleAccount string
}

type PGPConfig struct {
	Enabled         bool
	Sign            bool
	Encrypt         bool
	EncryptToSelf   bool
	AttachPublicKey bool
	PublicKey       string
	Identity        string
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	return parse(string(data))
}

func (c Config) PGPOptions() string {
	if !c.PGP.Enabled {
		return ""
	}
	options := []string{}
	if c.PGP.Encrypt {
		options = append(options, "encrypt")
		if c.PGP.EncryptToSelf {
			options = append(options, "self-encrypt")
		}
	}
	if c.PGP.Sign {
		options = append(options, "sign")
	}
	if c.PGP.AttachPublicKey {
		options = append(options, "attach-pubkey")
	}
	return strings.Join(options, ",")
}

func parse(data string) (Config, error) {
	cfg := Config{}
	section := ""
	for i, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("config line %d: expected key = value", i+1)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if err := applyValue(&cfg, section, key, value); err != nil {
			return Config{}, fmt.Errorf("config line %d: %w", i+1, err)
		}
	}
	return cfg, nil
}

func applyValue(cfg *Config, section, key, value string) error {
	switch section {
	case "ui":
		switch key {
		case "split":
			cfg.UI.Split = parseString(value)
		case "page_size":
			return parseInt(value, &cfg.UI.PageSize)
		case "editor":
			cfg.UI.Editor = parseString(value)
		}
	case "theme":
		switch key {
		case "header":
			cfg.Theme.Header = parseString(value)
		case "label", "mail_header":
			cfg.Theme.Label = parseString(value)
		case "selected":
			cfg.Theme.Selected = parseString(value)
		case "unread":
			cfg.Theme.Unread = parseString(value)
		case "status":
			cfg.Theme.Status = parseString(value)
		case "error":
			cfg.Theme.Error = parseString(value)
		case "divider":
			cfg.Theme.Divider = parseString(value)
		case "tag":
			cfg.Theme.Tag = parseString(value)
		case "spam":
			cfg.Theme.Spam = parseString(value)
		case "dim":
			cfg.Theme.Dim = parseString(value)
		case "link":
			cfg.Theme.Link = parseString(value)
		}
	case "keys":
		switch key {
		case "quit":
			cfg.Keys.Quit = parseString(value)
		case "force_quit":
			cfg.Keys.ForceQuit = parseString(value)
		case "sync":
			cfg.Keys.Sync = parseString(value)
		case "compose":
			cfg.Keys.Compose = parseString(value)
		case "filter":
			cfg.Keys.Filter = parseString(value)
		case "actions":
			cfg.Keys.Actions = parseString(value)
		case "open":
			cfg.Keys.Open = parseString(value)
		case "next":
			cfg.Keys.Next = parseString(value)
		case "prev":
			cfg.Keys.Prev = parseString(value)
		case "down":
			cfg.Keys.Down = parseString(value)
		case "up":
			cfg.Keys.Up = parseString(value)
		case "page_down":
			cfg.Keys.PageDown = parseString(value)
		case "page_up":
			cfg.Keys.PageUp = parseString(value)
		case "top":
			cfg.Keys.Top = parseString(value)
		case "bottom":
			cfg.Keys.Bottom = parseString(value)
		case "cycle_account":
			cfg.Keys.CycleAccount = parseString(value)
		}
	case "crypto":
		if key == "gpg_recipient" {
			cfg.PGP.Identity = parseString(value)
		}
	case "pgp":
		switch key {
		case "enabled":
			return parseBool(value, &cfg.PGP.Enabled)
		case "sign":
			return parseBool(value, &cfg.PGP.Sign)
		case "encrypt":
			return parseBool(value, &cfg.PGP.Encrypt)
		case "encrypt_to_self":
			return parseBool(value, &cfg.PGP.EncryptToSelf)
		case "attach_public_key":
			return parseBool(value, &cfg.PGP.AttachPublicKey)
		case "public_key":
			cfg.PGP.PublicKey = parseString(value)
		case "identity":
			cfg.PGP.Identity = parseString(value)
		}
	}
	return nil
}

func stripComment(line string) string {
	quoted := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			quoted = !quoted
			continue
		}
		if r == '#' && !quoted {
			return line[:i]
		}
	}
	return line
}

func parseString(value string) string {
	value = strings.TrimSpace(value)
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return strings.Trim(value, `"`)
}

func parseInt(value string, target *int) error {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("expected integer")
	}
	*target = parsed
	return nil
}

func parseBool(value string, target *bool) error {
	switch strings.ToLower(parseString(value)) {
	case "true":
		*target = true
		return nil
	case "false":
		*target = false
		return nil
	default:
		return fmt.Errorf("expected boolean")
	}
}
