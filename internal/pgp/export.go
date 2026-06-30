package pgp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func ExportAllPublicKeys() ([]byte, error) {
	return gpgExport("export public keys", "--batch", "--yes", "--armor", "--export")
}

func ExportAllSecretKeys() ([]byte, error) {
	return gpgExport("export secret keys", "--batch", "--yes", "--armor", "--export-secret-keys")
}

func ExportOwnerTrust() ([]byte, error) {
	return gpgExport("export ownertrust", "--batch", "--export-ownertrust")
}

func gpgExport(action string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("pgp: %s timed out", action)
		}
		return nil, fmt.Errorf("pgp: %s failed: %s", action, oneLine(stderr.String()))
	}
	return out, nil
}

func SecretKeyRecipient(data []byte) (string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return "", fmt.Errorf("pgp: no secret keys in export")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", "--batch", "--with-colons", "--import-options", "show-only", "--import")
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("pgp: inspect secret keys timed out")
		}
		return "", fmt.Errorf("pgp: inspect secret keys failed: %s", oneLine(stderr.String()))
	}
	wantFingerprint := false
	fallback := ""
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "sec":
			wantFingerprint = true
			if fallback == "" {
				fallback = normalizeKeyID(fields[4])
			}
		case "ssb":
			if fallback == "" {
				fallback = normalizeKeyID(fields[4])
			}
		case "fpr":
			if wantFingerprint {
				if fingerprint := normalizeKeyID(fields[9]); fingerprint != "" {
					return fingerprint, nil
				}
			}
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("pgp: no secret key identity in export")
}

func ImportKeyData(data []byte) error {
	return gpgImport("import keys", data, "--batch", "--yes", "--import")
}

func ImportOwnerTrustData(data []byte) error {
	return gpgImport("import ownertrust", data, "--batch", "--import-ownertrust")
}

func gpgImport(action string, data []byte, args ...string) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", args...)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("pgp: %s timed out", action)
		}
		return fmt.Errorf("pgp: %s failed: %s", action, oneLine(stderr.String()))
	}
	return nil
}
