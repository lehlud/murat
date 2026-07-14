package app

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/store"
)

type exportData struct {
	Accounts       []store.Account
	KnownAddresses []store.KnownAddress
	PublicKeys     []byte
	SecretKeys     []byte
}

type exportManifest struct {
	Version             int      `json:"version"`
	CreatedAt           string   `json:"created_at"`
	MuratVersion        string   `json:"murat_version"`
	Accounts            int      `json:"accounts"`
	KnownAddresses      int      `json:"known_addresses"`
	PublicKeyBytes      int      `json:"public_key_bytes"`
	SecretKeyBytes      int      `json:"secret_key_bytes"`
	SymmetricEncryption string   `json:"symmetric_encryption"`
	Files               []string `json:"files"`
}

var (
	collectExportData         = defaultCollectExportData
	encryptExportData         = nativeEncryptExport
	promptBackupPassphrase    = readBackupPassphrase
	promptNewBackupPassphrase = readNewBackupPassphrase
)

func cmdExport(args []string) error {
	fs := commandFlagSet("export", usageExport)
	force := fs.Bool("force", false, "overwrite output file")
	if handled, err := parseFlags(fs, args); handled || err != nil {
		return err
	}
	if fs.NArg() != 1 {
		usageExport(fs)
		return fmt.Errorf("output file required")
	}
	output := fs.Arg(0)
	if output == "-" {
		return fmt.Errorf("output file path required")
	}
	if !*force {
		if err := ensureExportOutputMissing(output); err != nil {
			return err
		}
	}
	data, err := collectExportData()
	if err != nil {
		return err
	}
	if err := encryptExportData(output, *force, data); err != nil {
		return err
	}
	fmt.Printf("exported %s\n", output)
	return nil
}

func usageExport(fs *flag.FlagSet) {
	usageFlags("export [flags] OUTPUT", "export accounts, known addresses, and managed PGP keys to an encrypted archive", fs)
}

func ensureExportOutputMissing(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return fmt.Errorf("output exists: %s (use --force to overwrite)", path)
	}
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func defaultCollectExportData() (exportData, error) {
	paths := store.DefaultPaths()
	key, err := loadLocalStoreKey(paths)
	if err != nil {
		return exportData{}, fmt.Errorf("not initialized: %w", err)
	}
	accountsStore, err := store.NewAccountStore(paths, key)
	if err != nil {
		return exportData{}, err
	}
	accounts, err := accountsStore.All()
	if err != nil {
		return exportData{}, err
	}
	if accounts == nil {
		accounts = []store.Account{}
	}
	s, err := store.Open(paths, key)
	if err != nil {
		return exportData{}, err
	}
	known := s.KnownAddresses()
	if known == nil {
		known = []store.KnownAddress{}
	}
	publicKeys, err := pgp.ExportAllPublicKeys()
	if err != nil {
		return exportData{}, err
	}
	secretKeys, err := pgp.ExportAllSecretKeys()
	if err != nil {
		return exportData{}, err
	}
	return exportData{
		Accounts:       accounts,
		KnownAddresses: known,
		PublicKeys:     publicKeys,
		SecretKeys:     secretKeys,
	}, nil
}

func writeExportTar(w io.Writer, data exportData) error {
	return writeExportTarAt(w, data, time.Now().UTC())
}

func writeExportTarAt(w io.Writer, data exportData, now time.Time) error {
	files := []string{
		"manifest.json",
		"accounts.json",
		"known-addresses.json",
		"pgp/public.asc",
		"pgp/secret.asc",
	}
	manifest := exportManifest{
		Version:             2,
		CreatedAt:           now.Format(time.RFC3339),
		MuratVersion:        commit,
		Accounts:            len(data.Accounts),
		KnownAddresses:      len(data.KnownAddresses),
		PublicKeyBytes:      len(data.PublicKeys),
		SecretKeyBytes:      len(data.SecretKeys),
		SymmetricEncryption: "argon2id + aes-256-gcm",
		Files:               files,
	}
	tw := tar.NewWriter(w)
	if err := writeJSONTarFile(tw, "manifest.json", manifest, now); err != nil {
		return err
	}
	if err := writeJSONTarFile(tw, "accounts.json", store.AccountIndex{Version: 1, Accounts: data.Accounts}, now); err != nil {
		return err
	}
	if err := writeJSONTarFile(tw, "known-addresses.json", data.KnownAddresses, now); err != nil {
		return err
	}
	if err := writeTarFile(tw, "pgp/public.asc", data.PublicKeys, 0o600, now); err != nil {
		return err
	}
	if err := writeTarFile(tw, "pgp/secret.asc", data.SecretKeys, 0o600, now); err != nil {
		return err
	}
	return tw.Close()
}

func writeJSONTarFile(tw *tar.Writer, name string, value any, now time.Time) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeTarFile(tw, name, data, 0o600, now)
}

func writeTarFile(tw *tar.Writer, name string, data []byte, mode int64, now time.Time) error {
	header := &tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: now,
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func passphraseFD(passphrase []byte) (*os.File, func(), error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, func() {}, err
	}
	buf := append([]byte(nil), passphrase...)
	buf = append(buf, '\n')
	clear := func() {
		clearBytes(buf)
		_ = r.Close()
	}
	if _, err := w.Write(buf); err != nil {
		_ = w.Close()
		clear()
		return nil, func() {}, err
	}
	if err := w.Close(); err != nil {
		clear()
		return nil, func() {}, err
	}
	return r, clear, nil
}

func clearBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func validBackupPassphrase(passphrase []byte) error {
	if len(bytes.TrimSpace(passphrase)) == 0 {
		return fmt.Errorf("backup passphrase cannot be empty")
	}
	return nil
}
