package store

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type keyFile struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
}

func EnsureKey(paths Paths, gpgRecipient string) ([]byte, error) {
	if data, err := LoadKey(paths); err == nil {
		return data, nil
	}
	if gpgRecipient == "" {
		return nil, fmt.Errorf("key missing: run `murat init --gpg-key KEY` or initialize Python murat first")
	}
	key, err := newStoreKey()
	if err != nil {
		return nil, err
	}
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	blob, err := gpgEncrypt(gpgRecipient, key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(paths.KeyFile, blob, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func EnsureRawKey(paths Paths) ([]byte, error) {
	if data, err := LoadKey(paths); err == nil {
		return data, nil
	}
	key, err := newStoreKey()
	if err != nil {
		return nil, err
	}
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	if err := writeRawKey(paths, key); err != nil {
		return nil, err
	}
	return key, nil
}

func newStoreKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func writeRawKey(paths Paths, key []byte) error {
	blob, err := json.Marshal(keyFile{Version: 1, Kind: "raw", Key: base64.RawURLEncoding.EncodeToString(key)})
	if err != nil {
		return err
	}
	blob = append(blob, '\n')
	return os.WriteFile(paths.KeyFile, blob, 0o600)
}

func LoadKey(paths Paths) ([]byte, error) {
	encoded, err := os.ReadFile(paths.KeyFile)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 0 && encoded[0] == '{' {
		var file keyFile
		if err := json.Unmarshal(encoded, &file); err != nil {
			return nil, err
		}
		data, err := base64.RawURLEncoding.DecodeString(file.Key)
		if err != nil {
			return nil, err
		}
		switch file.Kind {
		case "raw":
			return data, nil
		case "gpg":
			return gpgDecrypt(data)
		default:
			return nil, fmt.Errorf("unsupported key kind %q", file.Kind)
		}
	}
	return gpgDecrypt(encoded)
}

func gpgEncrypt(recipient string, data []byte) ([]byte, error) {
	cmd := exec.Command("gpg", "--batch", "--yes", "--encrypt", "--recipient", recipient)
	cmd.Stdin = bytesReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gpg encrypt failed: %s", gpgErrorMessage(stderr.String(), err))
	}
	return out, nil
}

func gpgDecrypt(data []byte) ([]byte, error) {
	cmd := exec.Command("gpg", "--quiet", "--decrypt")
	cmd.Stdin = bytesReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gpg decrypt failed: %s", gpgErrorMessage(stderr.String(), err))
	}
	return out, nil
}

func gpgErrorMessage(stderr string, err error) string {
	stderr = strings.TrimSpace(strings.ReplaceAll(stderr, "\r", "\n"))
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}

func bytesReader(data []byte) io.Reader { return bytes.NewReader(data) }
