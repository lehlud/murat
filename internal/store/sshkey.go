package store

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var ErrSSHKeyMissing = errors.New("SSH-wrapped local key missing")

type SSHKeyCandidate struct {
	Path        string
	PublicKey   string
	Fingerprint string
	Comment     string
}

type sshKeyEnvelope struct {
	Version        int    `json:"v"`
	Kind           string `json:"kind"`
	PublicKey      string `json:"public_key"`
	PrivateKeyPath string `json:"private_key_path"`
	Salt           string `json:"salt"`
	Nonce          string `json:"nonce"`
	Ciphertext     string `json:"ciphertext"`
}

func FindSSHKeyCandidates() ([]SSHKeyCandidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(home, ".ssh", "*.pub"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	items := []SSHKeyCandidate{}
	for _, publicPath := range paths {
		privatePath := strings.TrimSuffix(publicPath, ".pub")
		if info, err := os.Stat(privatePath); err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(publicPath)
		if err != nil {
			continue
		}
		key, comment, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil || key.Type() != ssh.KeyAlgoED25519 {
			continue
		}
		fingerprint := ssh.FingerprintSHA256(key)
		if seen[fingerprint] {
			continue
		}
		seen[fingerprint] = true
		items = append(items, SSHKeyCandidate{
			Path: privatePath, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
			Fingerprint: fingerprint, Comment: comment,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

func CreateSSHKey(paths Paths, privateKeyPath string, prompt func(string) ([]byte, error)) ([]byte, error) {
	if key, err := LoadSSHKey(paths, prompt); err == nil {
		return key, nil
	} else if !errors.Is(err, ErrSSHKeyMissing) {
		return nil, err
	}
	candidate, err := SSHKeyCandidateForPath(privateKeyPath)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := writeSSHKey(paths, candidate, key, prompt); err != nil {
		return nil, err
	}
	return key, nil
}

func LoadSSHKey(paths Paths, prompt func(string) ([]byte, error)) ([]byte, error) {
	data, err := os.ReadFile(paths.KeyFile)
	if os.IsNotExist(err) {
		return nil, ErrSSHKeyMissing
	}
	if err != nil {
		return nil, err
	}
	var env sshKeyEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("read SSH key envelope: %w", err)
	}
	if env.Version != 1 || env.Kind != "ssh-ed25519-signature" {
		return nil, fmt.Errorf("unsupported local key envelope")
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(env.PublicKey))
	if err != nil || publicKey.Type() != ssh.KeyAlgoED25519 {
		return nil, fmt.Errorf("invalid SSH public key in local key envelope")
	}
	salt, err := decodeKeyField(env.Salt)
	if err != nil {
		return nil, err
	}
	nonce, err := decodeKeyField(env.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := decodeKeyField(env.Ciphertext)
	if err != nil {
		return nil, err
	}
	signature, err := sshKeySignature(publicKey, env.PrivateKeyPath, salt, prompt)
	if err != nil {
		return nil, err
	}
	defer clearKeyBytes(signature)
	wrapKey := deriveSSHWrapKey(signature, salt)
	defer clearKeyBytes(wrapKey)
	block, err := aes.NewCipher(wrapKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, []byte(env.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("unlock local key: %w", err)
	}
	if len(plain) != 32 {
		clearKeyBytes(plain)
		return nil, fmt.Errorf("invalid local key")
	}
	return plain, nil
}

func SSHKeyCandidateForPath(path string) (SSHKeyCandidate, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return SSHKeyCandidate{}, fmt.Errorf("SSH key path required")
	}
	data, err := os.ReadFile(path + ".pub")
	if err != nil {
		return SSHKeyCandidate{}, fmt.Errorf("read SSH public key: %w", err)
	}
	key, comment, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return SSHKeyCandidate{}, fmt.Errorf("parse SSH public key: %w", err)
	}
	if key.Type() != ssh.KeyAlgoED25519 {
		return SSHKeyCandidate{}, fmt.Errorf("SSH key must be Ed25519")
	}
	return SSHKeyCandidate{Path: path, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), Fingerprint: ssh.FingerprintSHA256(key), Comment: comment}, nil
}

func writeSSHKey(paths Paths, candidate SSHKeyCandidate, key []byte, prompt func(string) ([]byte, error)) error {
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(candidate.PublicKey))
	if err != nil {
		return err
	}
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}
	signature, err := sshKeySignature(publicKey, candidate.Path, salt, prompt)
	if err != nil {
		return err
	}
	defer clearKeyBytes(signature)
	wrapKey := deriveSSHWrapKey(signature, salt)
	defer clearKeyBytes(wrapKey)
	block, err := aes.NewCipher(wrapKey)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := aead.Seal(nil, nonce, key, []byte(candidate.PublicKey))
	env, err := json.Marshal(sshKeyEnvelope{Version: 1, Kind: "ssh-ed25519-signature", PublicKey: candidate.PublicKey, PrivateKeyPath: candidate.Path, Salt: base64.RawURLEncoding.EncodeToString(salt), Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)})
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(paths.DataDir, ".key.ssh.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(env, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, paths.KeyFile)
}

func sshKeySignature(publicKey ssh.PublicKey, privatePath string, salt []byte, prompt func(string) ([]byte, error)) ([]byte, error) {
	message := append([]byte("murat SSH local key v1\x00"), salt...)
	if signature, ok := agentSignature(publicKey, message); ok {
		return signature, nil
	}
	privateData, err := os.ReadFile(privatePath)
	if err != nil {
		return nil, fmt.Errorf("read SSH private key: %w", err)
	}
	raw, err := ssh.ParseRawPrivateKey(privateData)
	if _, ok := err.(*ssh.PassphraseMissingError); ok {
		if prompt == nil {
			return nil, fmt.Errorf("SSH key passphrase required")
		}
		passphrase, promptErr := prompt(privatePath)
		if promptErr != nil {
			return nil, promptErr
		}
		defer clearKeyBytes(passphrase)
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase(privateData, passphrase)
	}
	if err != nil {
		return nil, fmt.Errorf("parse SSH private key: %w", err)
	}
	private, ok := raw.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("SSH key must be Ed25519")
	}
	derived, err := ssh.NewPublicKey(private.Public())
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(derived.Marshal(), publicKey.Marshal()) {
		return nil, fmt.Errorf("SSH private key does not match selected public key")
	}
	return ed25519.Sign(private, message), nil
}

func agentSignature(publicKey ssh.PublicKey, message []byte) ([]byte, bool) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, false
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, false
	}
	defer conn.Close()
	client := agent.NewClient(conn)
	keys, err := client.List()
	if err != nil {
		return nil, false
	}
	for _, key := range keys {
		if !bytes.Equal(key.Marshal(), publicKey.Marshal()) {
			continue
		}
		signature, err := client.Sign(key, message)
		if err != nil || signature.Format != ssh.KeyAlgoED25519 || len(signature.Blob) != ed25519.SignatureSize {
			return nil, false
		}
		return append([]byte(nil), signature.Blob...), true
	}
	return nil, false
}

func deriveSSHWrapKey(signature, salt []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(signature)
	_, _ = mac.Write([]byte("murat SSH local key wrap v1"))
	return mac.Sum(nil)
}

func decodeKeyField(value string) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid local key envelope: %w", err)
	}
	return data, nil
}

func clearKeyBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
