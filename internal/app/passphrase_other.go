//go:build !linux

package app

import "fmt"

func readBackupPassphrase() ([]byte, error) {
	return nil, fmt.Errorf("backup passphrase prompt is not supported on this platform")
}

func readNewBackupPassphrase() ([]byte, error) {
	return nil, fmt.Errorf("backup passphrase prompt is not supported on this platform")
}
