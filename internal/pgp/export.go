package pgp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
