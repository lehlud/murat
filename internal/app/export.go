package app

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/store"
)

type exportData struct {
	Accounts       []store.Account
	KnownAddresses []store.KnownAddress
	PublicKeys     []byte
	SecretKeys     []byte
	OwnerTrust     []byte
}

type exportManifest struct {
	Version             int      `json:"version"`
	CreatedAt           string   `json:"created_at"`
	MuratVersion        string   `json:"murat_version"`
	Accounts            int      `json:"accounts"`
	KnownAddresses      int      `json:"known_addresses"`
	PublicKeyBytes      int      `json:"public_key_bytes"`
	SecretKeyBytes      int      `json:"secret_key_bytes"`
	OwnerTrustBytes     int      `json:"ownertrust_bytes"`
	SymmetricEncryption string   `json:"symmetric_encryption"`
	Files               []string `json:"files"`
}

var (
	collectExportData         = defaultCollectExportData
	encryptExportData         = gpgSymmetricEncryptExport
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
	usageFlags("export [flags] OUTPUT", "export accounts, known addresses, and GPG keys to a symmetric GPG-encrypted tar archive", fs)
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
	key, err := store.LoadKey(paths)
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
	ownerTrust, err := pgp.ExportOwnerTrust()
	if err != nil {
		return exportData{}, err
	}
	return exportData{
		Accounts:       accounts,
		KnownAddresses: known,
		PublicKeys:     publicKeys,
		SecretKeys:     secretKeys,
		OwnerTrust:     ownerTrust,
	}, nil
}

func gpgSymmetricEncryptExport(output string, force bool, data exportData) error {
	passphrase, err := promptNewBackupPassphrase()
	if err != nil {
		return err
	}
	defer clearBytes(passphrase)

	dir := filepath.Dir(output)
	base := filepath.Base(output)
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	defer os.Remove(tmpPath)

	passphraseFile, cleanupPassphrase, err := passphraseFD(passphrase)
	if err != nil {
		return err
	}
	defer cleanupPassphrase()

	cmd := exec.Command("gpg", gpgSymmetricEncryptArgs(tmpPath)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{passphraseFile}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	writeErr := writeExportTar(stdin, data)
	closeErr := stdin.Close()
	waitErr := cmd.Wait()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if waitErr != nil {
		return fmt.Errorf("gpg symmetric encrypt failed: %w", waitErr)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return installEncryptedExport(tmpPath, output, force)
}

func gpgSymmetricEncryptArgs(output string) []string {
	return []string{"--batch", "--yes", "--pinentry-mode", "loopback", "--passphrase-fd", "3", "--symmetric", "--cipher-algo", "AES256", "--output", output}
}

func installEncryptedExport(tmpPath, output string, force bool) error {
	in, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	defer in.Close()

	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	out, err := os.OpenFile(output, flags, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("output exists: %s (use --force to overwrite)", output)
		}
		return err
	}
	copied := false
	defer func() {
		_ = out.Close()
		if !copied {
			_ = os.Remove(output)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Chmod(0o600); err != nil {
		return err
	}
	copied = true
	return nil
}

func writeExportTar(w io.Writer, data exportData) error {
	return writeExportTarAt(w, data, time.Now().UTC())
}

func writeExportTarAt(w io.Writer, data exportData, now time.Time) error {
	files := []string{
		"manifest.json",
		"accounts.json",
		"known-addresses.json",
		"gpg/public.asc",
		"gpg/secret.asc",
		"gpg/ownertrust.txt",
	}
	manifest := exportManifest{
		Version:             1,
		CreatedAt:           now.Format(time.RFC3339),
		MuratVersion:        commit,
		Accounts:            len(data.Accounts),
		KnownAddresses:      len(data.KnownAddresses),
		PublicKeyBytes:      len(data.PublicKeys),
		SecretKeyBytes:      len(data.SecretKeys),
		OwnerTrustBytes:     len(data.OwnerTrust),
		SymmetricEncryption: "gpg --symmetric --cipher-algo AES256",
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
	if err := writeTarFile(tw, "gpg/public.asc", data.PublicKeys, 0o600, now); err != nil {
		return err
	}
	if err := writeTarFile(tw, "gpg/secret.asc", data.SecretKeys, 0o600, now); err != nil {
		return err
	}
	if err := writeTarFile(tw, "gpg/ownertrust.txt", data.OwnerTrust, 0o600, now); err != nil {
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
