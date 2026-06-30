package app

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/store"
)

type importOptions struct {
	GPGRecipient    string
	ReplaceAccounts bool
}

type importSummary struct {
	Accounts        int
	KnownAddresses  int
	PublicKeyBytes  int
	SecretKeyBytes  int
	OwnerTrustBytes int
}

var (
	readImportData  = gpgDecryptImportData
	applyImportData = defaultApplyImportData
)

func cmdImportArchive(args []string) error {
	fs := commandFlagSet("import", usageImport)
	gpgKey := fs.String("gpg-key", "", "GPG recipient used to wrap local store key if missing")
	replaceAccounts := fs.Bool("replace-accounts", false, "replace account store instead of merging")
	if handled, err := parseFlags(fs, args); handled || err != nil {
		return err
	}
	if fs.NArg() != 1 {
		usageImport(fs)
		return fmt.Errorf("input file required")
	}
	input := fs.Arg(0)
	if input == "-" {
		return fmt.Errorf("input file path required")
	}
	data, err := readImportData(input)
	if err != nil {
		return err
	}
	summary, err := applyImportData(data, importOptions{GPGRecipient: *gpgKey, ReplaceAccounts: *replaceAccounts})
	if err != nil {
		return err
	}
	fmt.Printf("imported %d accounts, %d known addresses, %d public-key bytes, %d secret-key bytes\n", summary.Accounts, summary.KnownAddresses, summary.PublicKeyBytes, summary.SecretKeyBytes)
	return nil
}

func usageImport(fs *flag.FlagSet) {
	usageFlags("import [flags] BACKUP.tar.gpg", "import accounts, known addresses, and GPG keys from a symmetric GPG-encrypted export", fs)
}

func gpgDecryptImportData(path string) (exportData, error) {
	cmd := exec.Command("gpg", "--quiet", "--decrypt", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return exportData{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return exportData{}, err
	}
	data, readErr := readImportTar(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return exportData{}, fmt.Errorf("gpg decrypt failed: %s", importErrorLine(message))
	}
	if readErr != nil {
		return exportData{}, readErr
	}
	return data, nil
}

func importErrorLine(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", "\n")
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return value
}

func readImportTar(r io.Reader) (exportData, error) {
	tr := tar.NewReader(r)
	data := exportData{Accounts: []store.Account{}, KnownAddresses: []store.KnownAddress{}}
	manifestSeen := false
	accountsSeen := false
	addressesSeen := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return exportData{}, err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return exportData{}, err
		}
		switch header.Name {
		case "manifest.json":
			var manifest exportManifest
			if err := json.Unmarshal(body, &manifest); err != nil {
				return exportData{}, fmt.Errorf("manifest.json: %w", err)
			}
			if manifest.Version != 1 {
				return exportData{}, fmt.Errorf("unsupported export version %d", manifest.Version)
			}
			manifestSeen = true
		case "accounts.json":
			var index store.AccountIndex
			if err := json.Unmarshal(body, &index); err != nil {
				return exportData{}, fmt.Errorf("accounts.json: %w", err)
			}
			if index.Version > 1 {
				return exportData{}, fmt.Errorf("unsupported accounts version %d", index.Version)
			}
			data.Accounts = index.Accounts
			if data.Accounts == nil {
				data.Accounts = []store.Account{}
			}
			accountsSeen = true
		case "known-addresses.json":
			if err := json.Unmarshal(body, &data.KnownAddresses); err != nil {
				return exportData{}, fmt.Errorf("known-addresses.json: %w", err)
			}
			if data.KnownAddresses == nil {
				data.KnownAddresses = []store.KnownAddress{}
			}
			addressesSeen = true
		case "gpg/public.asc":
			data.PublicKeys = body
		case "gpg/secret.asc":
			data.SecretKeys = body
		case "gpg/ownertrust.txt":
			data.OwnerTrust = body
		}
	}
	if !manifestSeen {
		return exportData{}, fmt.Errorf("missing manifest.json")
	}
	if !accountsSeen {
		return exportData{}, fmt.Errorf("missing accounts.json")
	}
	if !addressesSeen {
		return exportData{}, fmt.Errorf("missing known-addresses.json")
	}
	return data, nil
}

func defaultApplyImportData(data exportData, options importOptions) (importSummary, error) {
	summary := importSummary{
		Accounts:        len(data.Accounts),
		KnownAddresses:  len(data.KnownAddresses),
		PublicKeyBytes:  len(data.PublicKeys),
		SecretKeyBytes:  len(data.SecretKeys),
		OwnerTrustBytes: len(data.OwnerTrust),
	}
	paths := store.DefaultPaths()
	key, keyErr := store.LoadKey(paths)
	if keyErr != nil && strings.TrimSpace(options.GPGRecipient) == "" {
		return summary, fmt.Errorf("not initialized: %w (run `murat init --gpg-key KEY` or pass `--gpg-key KEY`)", keyErr)
	}
	if err := pgp.ImportKeyData(data.PublicKeys); err != nil {
		return summary, err
	}
	if err := pgp.ImportKeyData(data.SecretKeys); err != nil {
		return summary, err
	}
	if err := pgp.ImportOwnerTrustData(data.OwnerTrust); err != nil {
		return summary, err
	}
	if keyErr != nil {
		var err error
		key, err = store.EnsureKey(paths, strings.TrimSpace(options.GPGRecipient))
		if err != nil {
			return summary, err
		}
	}
	accountsStore, err := store.NewAccountStore(paths, key)
	if err != nil {
		return summary, err
	}
	if options.ReplaceAccounts {
		if err := accountsStore.Save(data.Accounts); err != nil {
			return summary, err
		}
	} else {
		existing, err := accountsStore.All()
		if err != nil {
			return summary, err
		}
		if err := accountsStore.Save(mergeImportedAccounts(existing, data.Accounts)); err != nil {
			return summary, err
		}
	}
	s, err := store.Open(paths, key)
	if err != nil {
		return summary, err
	}
	s.ImportKnownAddresses(data.KnownAddresses)
	if err := s.Flush(); err != nil {
		return summary, err
	}
	return summary, nil
}

func mergeImportedAccounts(existing, imported []store.Account) []store.Account {
	out := append([]store.Account(nil), existing...)
	for _, account := range imported {
		replaced := false
		if strings.TrimSpace(account.ID) != "" {
			for i := range out {
				if out[i].ID == account.ID {
					out[i] = account
					replaced = true
					break
				}
			}
		}
		if !replaced {
			out = append(out, account)
		}
	}
	return out
}
