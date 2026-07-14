package store

import (
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDir    string
	ConfigFile   string
	DataDir      string
	KeyFile      string
	IndexFile    string
	AccountsFile string
	SearchFile   string
	PGPKeyFile   string
	BodyDir      string
	RawDir       string
}

func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	configDir := filepath.Join(configHome, "murat")
	dataDir := filepath.Join(dataHome, "murat")
	return Paths{
		ConfigDir:    configDir,
		ConfigFile:   filepath.Join(configDir, "config.toml"),
		DataDir:      dataDir,
		KeyFile:      filepath.Join(dataDir, "key.ssh.json"),
		IndexFile:    filepath.Join(dataDir, "mail.enc.json"),
		AccountsFile: filepath.Join(dataDir, "accounts.enc.json"),
		SearchFile:   filepath.Join(dataDir, "search.enc.json"),
		PGPKeyFile:   filepath.Join(dataDir, "pgp.enc"),
		BodyDir:      filepath.Join(dataDir, "eml"),
		RawDir:       filepath.Join(dataDir, "eml"),
	}
}

func (p Paths) EnsureDirs() error {
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.BodyDir, p.RawDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
