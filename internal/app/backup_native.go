package app

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	muratcrypto "lehnert.dev/murat/internal/crypto"

	"golang.org/x/crypto/argon2"
)

type backupEnvelope struct {
	Version int
	Salt    string
	Data    string
}

func nativeEncryptExport(output string, force bool, data exportData) error {
	passphrase, err := promptNewBackupPassphrase()
	if err != nil {
		return err
	}
	defer clearBytes(passphrase)
	var tarData bytes.Buffer
	if err := writeExportTar(&tarData, data); err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key := argon2.IDKey(passphrase, salt, 3, 64*1024, 2, 32)
	defer clearBytes(key)
	box, err := muratcrypto.NewBox(key)
	if err != nil {
		return err
	}
	sealed, err := box.Seal(tarData.Bytes())
	if err != nil {
		return err
	}
	dataOut, err := json.Marshal(backupEnvelope{Version: 2, Salt: base64.RawURLEncoding.EncodeToString(salt), Data: base64.RawURLEncoding.EncodeToString(sealed)})
	if err != nil {
		return err
	}
	return os.WriteFile(output, append([]byte("MURAT-BACKUP-2\n"), append(dataOut, '\n')...), 0o600)
}

func nativeDecryptImportData(path string) (exportData, error) {
	passphrase, err := promptBackupPassphrase()
	if err != nil {
		return exportData{}, err
	}
	defer clearBytes(passphrase)
	data, err := os.ReadFile(path)
	if err != nil {
		return exportData{}, err
	}
	const prefix = "MURAT-BACKUP-2\n"
	if !strings.HasPrefix(string(data), prefix) {
		return exportData{}, fmt.Errorf("unsupported backup format")
	}
	var env backupEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(data[len(prefix):]), &env); err != nil {
		return exportData{}, err
	}
	if env.Version != 2 {
		return exportData{}, fmt.Errorf("unsupported backup version %d", env.Version)
	}
	salt, err := base64.RawURLEncoding.DecodeString(env.Salt)
	if err != nil {
		return exportData{}, err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(env.Data)
	if err != nil {
		return exportData{}, err
	}
	key := argon2.IDKey(passphrase, salt, 3, 64*1024, 2, 32)
	defer clearBytes(key)
	box, err := muratcrypto.NewBox(key)
	if err != nil {
		return exportData{}, err
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return exportData{}, fmt.Errorf("backup decrypt failed")
	}
	defer clearBytes(plain)
	return readImportTar(bytes.NewReader(plain))
}
