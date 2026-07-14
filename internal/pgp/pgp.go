package pgp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	"lehnert.dev/murat/internal/crypto"
	"lehnert.dev/murat/internal/protocol"
	"lehnert.dev/murat/internal/store"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
	"golang.org/x/crypto/openpgp/clearsign"
	_ "golang.org/x/crypto/ripemd160" // register legacy OpenPGP recipient preference hash
)

type Availability struct{ Sign, Encrypt, SelfEncrypt, AttachPublicKey bool }
type ApplyDraftOptions struct {
	LoopbackPinentry bool
	Passphrase       []byte
}

var ErrPassphraseRequired = errors.New("pgp: passphrase required")

var ringState struct {
	sync.Mutex
	paths store.Paths
	key   []byte
}

func ActivateManagedHomeIfPresent() {}

func Configure(paths store.Paths, key []byte) {
	ringState.Lock()
	defer ringState.Unlock()
	ringState.paths = paths
	ringState.key = append(ringState.key[:0], key...)
}

func Create(email, name string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("pgp: email required")
	}
	ring, err := loadRing()
	if err != nil {
		return err
	}
	if entityFor(ring, email, false) != nil {
		return fmt.Errorf("pgp: key already exists for %s", email)
	}
	entity, err := openpgp.NewEntity(name, "", email, nil)
	if err != nil {
		return fmt.Errorf("pgp: create key: %w", err)
	}
	ring = append(ring, entity)
	return saveRing(ring)
}

func List() ([]string, error) {
	ring, err := loadRing()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, entity := range ring {
		for identity := range entity.Identities {
			out = append(out, identity)
		}
	}
	return out, nil
}

func ImportKeyData(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	incoming, err := readKeyRing(data)
	if err != nil {
		return fmt.Errorf("pgp: import keys: %w", err)
	}
	ring, err := loadRing()
	if err != nil {
		return err
	}
	for _, entity := range incoming {
		if entity.PrimaryKey == nil {
			continue
		}
		replaced := false
		for i, current := range ring {
			if current.PrimaryKey != nil && current.PrimaryKey.KeyId == entity.PrimaryKey.KeyId {
				ring[i] = entity
				replaced = true
				break
			}
		}
		if !replaced {
			ring = append(ring, entity)
		}
	}
	return saveRing(ring)
}

func ExportAllPublicKeys() ([]byte, error) {
	ring, err := loadRing()
	if err != nil {
		return nil, err
	}
	return armorPublic(ring)
}

func ExportAllSecretKeys() ([]byte, error) {
	ring, err := loadRing()
	if err != nil {
		return nil, err
	}
	return armorPrivate(ring)
}

func ExportPublicKey(identity string) ([]byte, error) {
	ring, err := loadRing()
	if err != nil {
		return nil, err
	}
	entity := entityFor(ring, identity, false)
	if entity == nil {
		return nil, fmt.Errorf("pgp: public key not found for %s", identity)
	}
	return armorPublic(openpgp.EntityList{entity})
}

func ProcessTextWithKeys(text string, attached [][]byte) (string, string, bool) {
	if !strings.Contains(text, "-----BEGIN PGP ") {
		return text, "", false
	}
	ring, err := loadRing()
	if err != nil {
		return text, "pgp: keyring unavailable", false
	}
	for _, data := range attached {
		if keys, err := readKeyRing(data); err == nil {
			ring = append(ring, keys...)
		}
	}
	if strings.Contains(text, "-----BEGIN PGP SIGNED MESSAGE-----") {
		block, rest := clearsign.Decode([]byte(text))
		if block == nil || len(bytes.TrimSpace(rest)) != 0 {
			return text, "pgp: verify failed", true
		}
		_, err := openpgp.CheckArmoredDetachedSignature(ring, bytes.NewReader(block.Bytes), block.ArmoredSignature.Body)
		if err != nil {
			return string(block.Bytes), "pgp: signature invalid", true
		}
		return string(block.Bytes), "pgp: signature valid", true
	}
	block, err := armor.Decode(strings.NewReader(text))
	if err != nil {
		return text, "pgp: decrypt failed", false
	}
	message, err := openpgp.ReadMessage(block.Body, ring, nil, nil)
	if err != nil {
		return text, "pgp: decrypt failed: " + oneLine(err.Error()), false
	}
	plain, err := io.ReadAll(message.UnverifiedBody)
	if err != nil {
		return text, "pgp: decrypt failed: " + oneLine(err.Error()), false
	}
	status := "pgp: decrypted"
	if message.IsSigned {
		if message.SignatureError != nil {
			status += "; signature invalid"
		} else {
			status += "; signature valid"
		}
	}
	return strings.TrimSpace(string(plain)), status, true
}

func ProcessText(text string) (string, string, bool) { return ProcessTextWithKeys(text, nil) }

func IsPublicKeyAttachment(name, contentType string, data []byte) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "pgp-keys") || strings.HasSuffix(name, ".asc") || strings.HasSuffix(name, ".pgp") || bytes.Contains(data, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----"))
}

func ApplyDraft(from string, draft protocol.Draft) (protocol.Draft, string, error) {
	return ApplyDraftWithOptions(from, draft, ApplyDraftOptions{})
}

func ApplyDraftWithOptions(from string, draft protocol.Draft, _ ApplyDraftOptions) (protocol.Draft, string, error) {
	options := ParseOptions(draft.PGP)
	encrypt := options["encrypt"] || options["encrypted"]
	sign := options["sign"] || options["signed"]
	self := options["self-encrypt"] || options["selfencrypt"] || options["self"]
	attach := options["attach-pubkey"] || options["attach-public-key"] || options["pubkey"]
	if self && !encrypt {
		return draft, "", fmt.Errorf("pgp: self-encrypt needs encrypt")
	}
	ring, err := loadRing()
	if err != nil {
		return draft, "", err
	}
	signer := entityFor(ring, from, true)
	if (sign || self || attach) && signer == nil {
		return draft, "", fmt.Errorf("pgp: no secret key for %s", from)
	}
	status := []string{}
	if attach {
		key, err := armorPublic(openpgp.EntityList{signer})
		if err != nil {
			return draft, "", err
		}
		draft.Attachments = append(draft.Attachments, protocol.Attachment{Filename: publicKeyFilename(from), ContentType: "application/pgp-keys", Data: key})
		status = append(status, "public key attached")
	}
	if !encrypt && !sign {
		draft.PGP = ""
		if len(status) == 0 {
			return draft, "", nil
		}
		return draft, "pgp: " + strings.Join(status, "; "), nil
	}
	recipients := []*openpgp.Entity{}
	if encrypt {
		for _, address := range draftRecipients(draft) {
			entity := entityFor(ring, address, false)
			if entity == nil {
				return draft, "", fmt.Errorf("pgp: missing public key for %s", address)
			}
			recipients = append(recipients, entity)
		}
		if len(recipients) == 0 {
			return draft, "", fmt.Errorf("pgp: encrypt needs recipient")
		}
		if self {
			recipients = append(recipients, signer)
			status = append(status, "self encrypted")
		}
	}
	if len(draft.Attachments) > 0 {
		entity := canonicalCRLF(protocol.MIMEEntity(draft))
		if encrypt {
			out, err := armoredEncrypt(entity, recipients, signerIf(sign, signer))
			if err != nil {
				return draft, "", err
			}
			draft.RawMIME = encryptedMIMEEntity(out)
		} else {
			var signature bytes.Buffer
			if err := openpgp.ArmoredDetachSign(&signature, signer, strings.NewReader(entity), nil); err != nil {
				return draft, "", err
			}
			draft.RawMIME = signedMIMEEntity(entity, signature.String())
		}
		draft.Body = ""
		draft.Attachments = nil
		draft.PGP = ""
		if encrypt {
			status = append([]string{"encrypted"}, status...)
		}
		if sign {
			status = append(status, "signed")
		}
		return draft, "pgp: " + strings.Join(status, "; "), nil
	}
	if encrypt {
		out, err := armoredEncrypt(draft.Body, recipients, signerIf(sign, signer))
		if err != nil {
			return draft, "", err
		}
		draft.Body = out
	} else {
		var out bytes.Buffer
		writer, err := clearsign.Encode(&out, signer.PrivateKey, nil)
		if err != nil {
			return draft, "", err
		}
		if _, err := writer.Write([]byte(draft.Body)); err != nil {
			return draft, "", err
		}
		if err := writer.Close(); err != nil {
			return draft, "", err
		}
		draft.Body = out.String()
	}
	draft.PGP = ""
	if encrypt {
		status = append([]string{"encrypted"}, status...)
	}
	if sign {
		status = append(status, "signed")
	}
	return draft, "pgp: " + strings.Join(status, "; "), nil
}

func DraftNeedsSigning(draft protocol.Draft) bool {
	options := ParseOptions(draft.PGP)
	return options["sign"] || options["signed"]
}
func SigningNeedsPassphrase(identity string) (bool, error) {
	if !HasSecretKey(identity) {
		return false, fmt.Errorf("pgp: no secret key for %s", identity)
	}
	return false, nil
}
func IsPassphraseRequired(error) bool { return false }

func CheckAvailability(from string, draft protocol.Draft) Availability {
	recipients := draftRecipients(draft)
	encrypt := len(recipients) > 0 && len(MissingPublicKeys(recipients)) == 0
	secret := HasSecretKey(from)
	return Availability{Sign: secret, Encrypt: encrypt, SelfEncrypt: secret && HasPublicKey(from) && encrypt, AttachPublicKey: secret && HasPublicKey(from)}
}
func HasSecretKey(identity string) bool {
	ring, err := loadRing()
	return err == nil && entityFor(ring, identity, true) != nil
}
func HasPublicKey(identity string) bool {
	ring, err := loadRing()
	return err == nil && entityFor(ring, identity, false) != nil
}
func MissingPublicKeys(recipients []string) []string {
	missing := []string{}
	seen := map[string]bool{}
	for _, recipient := range recipients {
		recipient = strings.TrimSpace(recipient)
		key := strings.ToLower(recipient)
		if recipient != "" && !seen[key] {
			seen[key] = true
			if !HasPublicKey(recipient) {
				missing = append(missing, recipient)
			}
		}
	}
	return missing
}

func loadRing() (openpgp.EntityList, error) {
	ringState.Lock()
	defer ringState.Unlock()
	if len(ringState.key) != 32 {
		return nil, fmt.Errorf("pgp: local store is not open")
	}
	data, err := os.ReadFile(ringState.paths.PGPKeyFile)
	if os.IsNotExist(err) {
		return openpgp.EntityList{}, nil
	}
	if err != nil {
		return nil, err
	}
	box, err := crypto.NewBox(ringState.key)
	if err != nil {
		return nil, err
	}
	plain, err := box.Open(data)
	if err != nil {
		return nil, err
	}
	defer clearBytes(plain)
	return openpgp.ReadKeyRing(bytes.NewReader(plain))
}
func saveRing(ring openpgp.EntityList) error {
	ringState.Lock()
	defer ringState.Unlock()
	if len(ringState.key) != 32 {
		return fmt.Errorf("pgp: local store is not open")
	}
	var raw bytes.Buffer
	for _, entity := range ring {
		if entity.PrivateKey != nil {
			if err := entity.SerializePrivate(&raw, nil); err != nil {
				return err
			}
		} else if err := entity.Serialize(&raw); err != nil {
			return err
		}
	}
	box, err := crypto.NewBox(ringState.key)
	if err != nil {
		return err
	}
	sealed, err := box.Seal(raw.Bytes())
	if err != nil {
		return err
	}
	if err := ringState.paths.EnsureDirs(); err != nil {
		return err
	}
	return os.WriteFile(ringState.paths.PGPKeyFile, sealed, 0o600)
}
func readKeyRing(data []byte) (openpgp.EntityList, error) {
	if strings.Contains(string(data), "-----BEGIN PGP") {
		return openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
	}
	return openpgp.ReadKeyRing(bytes.NewReader(data))
}
func armorPublic(ring openpgp.EntityList) ([]byte, error) {
	var out bytes.Buffer
	w, err := armor.Encode(&out, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	for _, e := range ring {
		if err := e.Serialize(w); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func armorPrivate(ring openpgp.EntityList) ([]byte, error) {
	var out bytes.Buffer
	w, err := armor.Encode(&out, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, err
	}
	for _, e := range ring {
		if e.PrivateKey != nil {
			if err := e.SerializePrivate(w, nil); err != nil {
				return nil, err
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func entityFor(ring openpgp.EntityList, identity string, secret bool) *openpgp.Entity {
	identity = strings.ToLower(strings.TrimSpace(identity))
	for _, entity := range ring {
		if secret && entity.PrivateKey == nil {
			continue
		}
		for value := range entity.Identities {
			address := identityAddress(value)
			if strings.EqualFold(value, identity) || strings.EqualFold(address, identity) {
				return entity
			}
		}
	}
	return nil
}
func identityAddress(value string) string {
	if address, err := mail.ParseAddress(value); err == nil {
		return strings.ToLower(address.Address)
	}
	return strings.ToLower(value)
}
func signerIf(ok bool, signer *openpgp.Entity) *openpgp.Entity {
	if ok {
		return signer
	}
	return nil
}
func armoredEncrypt(text string, recipients []*openpgp.Entity, signer *openpgp.Entity) (string, error) {
	var out bytes.Buffer
	armorWriter, err := armor.Encode(&out, "PGP MESSAGE", nil)
	if err != nil {
		return "", err
	}
	writer, err := openpgp.Encrypt(armorWriter, recipients, signer, nil, nil)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write([]byte(text)); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	if err := armorWriter.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}
func draftRecipients(d protocol.Draft) []string {
	out := []string{}
	for _, raw := range []string{d.To, d.Cc, d.Bcc} {
		for _, a := range strings.Split(raw, ",") {
			if parsed, err := mail.ParseAddress(strings.TrimSpace(a)); err == nil {
				out = append(out, parsed.Address)
			} else if strings.TrimSpace(a) != "" {
				out = append(out, strings.TrimSpace(a))
			}
		}
	}
	return out
}
func ParseOptions(value string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			out[part] = true
		}
	}
	return out
}
func canonicalCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}
func signedMIMEEntity(entity, signature string) string {
	var buf bytes.Buffer
	boundary := multipart.NewWriter(&buf).Boundary()
	return strings.Join([]string{"Content-Type: multipart/signed; protocol=\"application/pgp-signature\"; micalg=pgp-sha256; boundary=" + boundary, "", "--" + boundary, strings.TrimRight(canonicalCRLF(entity), "\r\n"), "--" + boundary, "Content-Type: application/pgp-signature; name=\"signature.asc\"", "Content-Disposition: attachment; filename=\"signature.asc\"", "Content-Transfer-Encoding: 7bit", "", strings.TrimSpace(signature), "--" + boundary + "--", ""}, "\r\n")
}
func encryptedMIMEEntity(encrypted string) string {
	var buf bytes.Buffer
	boundary := multipart.NewWriter(&buf).Boundary()
	return strings.Join([]string{"Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=" + boundary, "", "--" + boundary, "Content-Type: application/pgp-encrypted", "", "Version: 1", "--" + boundary, "Content-Type: application/octet-stream; name=\"encrypted.asc\"", "Content-Disposition: inline; filename=\"encrypted.asc\"", "Content-Transfer-Encoding: 7bit", "", strings.TrimSpace(encrypted), "--" + boundary + "--", ""}, "\r\n")
}
func publicKeyFilename(identity string) string {
	value := strings.NewReplacer("@", "_at_", ".", "_").Replace(strings.TrimSpace(identity))
	if value == "" {
		value = "public-key"
	}
	return value + ".asc"
}
func oneLine(value string) string { return strings.Join(strings.Fields(value), " ") }
func clearBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
func now() time.Time { return time.Now() }
