package pgp

import (
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lehnert.dev/murat/internal/protocol"
)

type Availability struct {
	Sign            bool
	Encrypt         bool
	SelfEncrypt     bool
	AttachPublicKey bool
}

func ProcessText(text string) (string, string, bool) {
	return ProcessTextWithKeys(text, nil)
}

func ProcessTextWithKeys(text string, publicKeys [][]byte) (string, string, bool) {
	encrypted := strings.Contains(text, "-----BEGIN PGP MESSAGE-----")
	signed := strings.Contains(text, "-----BEGIN PGP SIGNED MESSAGE-----")
	if !encrypted && !signed {
		return text, "", false
	}
	if signed && !encrypted {
		return processClearSignedText(text, publicKeys)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", "--batch", "--yes", "--decrypt")
	cmd.Stdin = strings.NewReader(text)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return text, "pgp: decrypt timed out", false
	}
	if err != nil {
		return text, "pgp: decrypt failed: " + oneLine(stderr.String()), false
	}
	return strings.TrimSpace(stdout.String()), pgpStatus(stderr.String(), encrypted), true
}

func processClearSignedText(text string, publicKeys [][]byte) (string, string, bool) {
	clearText, ok := clearSignedText(text)
	if !ok {
		clearText = text
	}
	result := runGPGVerify(text, "")
	identity := signatureIdentity(text, result)
	if result.timedOut {
		return clearText, "pgp: verify timed out", true
	}
	if result.err != nil {
		status := pgpFailureStatus("verify", result.output(), result.err)
		if isMissingPublicKeyStatus(status, result.output()) {
			if localStatus, ok := verifyWithLocalKey(text, identity); ok {
				return clearText, localStatus, true
			}
			if attachedStatus, ok := verifyWithAttachedPublicKeys(text, publicKeys, identity); ok {
				return clearText, attachedStatus, true
			}
		}
		return clearText, status, true
	}
	return clearText, pgpStatus(result.output(), false), true
}

func clearSignedText(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "-----BEGIN PGP SIGNED MESSAGE-----" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	for start < len(lines) && strings.TrimSpace(lines[start]) != "" {
		start++
	}
	if start >= len(lines) {
		return "", false
	}
	start++
	out := []string{}
	for ; start < len(lines); start++ {
		line := lines[start]
		if strings.TrimSpace(line) == "-----BEGIN PGP SIGNATURE-----" {
			return strings.TrimSpace(strings.Join(out, "\n")), true
		}
		if strings.HasPrefix(line, "- ") {
			line = strings.TrimPrefix(line, "- ")
		}
		out = append(out, line)
	}
	return "", false
}

type gpgVerifyResult struct {
	status   string
	stderr   string
	err      error
	timedOut bool
}

func (r gpgVerifyResult) output() string {
	return strings.TrimSpace(r.status + "\n" + r.stderr)
}

type signatureID struct {
	fingerprint string
	keyID       string
}

func runGPGVerify(text, homedir string) gpgVerifyResult {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := []string{"--batch", "--yes", "--status-fd", "1"}
	if homedir != "" {
		args = append(args, "--homedir", homedir)
	}
	args = append(args, "--verify")
	cmd := exec.CommandContext(ctx, "gpg", args...)
	cmd.Stdin = strings.NewReader(text)
	var status bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &status
	cmd.Stderr = &stderr
	err := cmd.Run()
	return gpgVerifyResult{status: status.String(), stderr: stderr.String(), err: err, timedOut: ctx.Err() == context.DeadlineExceeded}
}

func verifyWithLocalKey(text string, identity signatureID) (string, bool) {
	lookup := identity.lookupValues()
	if len(lookup) == 0 {
		return "", false
	}
	for _, value := range lookup {
		key, err := ExportPublicKey(value)
		if err != nil {
			continue
		}
		status, ok := verifyWithPublicKeyData(text, [][]byte{key}, "local key", localTrustStatus(value))
		if ok {
			return status, true
		}
	}
	return "", false
}

func verifyWithAttachedPublicKeys(text string, publicKeys [][]byte, identity signatureID) (string, bool) {
	if len(publicKeys) == 0 {
		return "", false
	}
	return verifyWithPublicKeyData(text, matchingPublicKeys(publicKeys, identity), "attached public key", "untrusted")
}

func verifyWithPublicKeyData(text string, publicKeys [][]byte, source, trust string) (string, bool) {
	if len(publicKeys) == 0 {
		return "", false
	}
	dir, err := os.MkdirTemp("", "murat-pgp-verify-*")
	if err != nil {
		return "", false
	}
	defer os.RemoveAll(dir)
	imported := 0
	for _, key := range publicKeys {
		if importPublicKey(dir, key) == nil {
			imported++
		}
	}
	if imported == 0 {
		return "", false
	}
	result := runGPGVerify(text, dir)
	suffix := " with " + source
	if trust != "" {
		suffix += " (" + trust + ")"
	}
	if result.timedOut {
		return "pgp: verify timed out" + suffix, true
	}
	status := pgpStatus(result.output(), false)
	if result.err == nil {
		if strings.Contains(strings.ToLower(status), "signature valid") {
			return "pgp: signature valid" + suffix, true
		}
		return "pgp: signature verified" + suffix, true
	}
	if strings.Contains(strings.ToLower(status), "signature bad") {
		return "pgp: signature BAD" + suffix, true
	}
	if isMissingPublicKeyStatus(status, result.output()) {
		return "", false
	}
	if status != "" {
		return status + suffix, true
	}
	return "", false
}

func matchingPublicKeys(publicKeys [][]byte, identity signatureID) [][]byte {
	if identity.empty() {
		return publicKeys
	}
	out := [][]byte{}
	for _, key := range publicKeys {
		ids := publicKeyIdentities(key)
		if ids.matches(identity) {
			out = append(out, key)
		}
	}
	return out
}

func importPublicKey(homedir string, key []byte) error {
	if len(bytes.TrimSpace(key)) == 0 {
		return fmt.Errorf("empty key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", "--batch", "--yes", "--homedir", homedir, "--import")
	cmd.Stdin = bytes.NewReader(key)
	return cmd.Run()
}

func IsPublicKeyAttachment(filename, contentType string, data []byte) bool {
	if bytes.Contains(data, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")) {
		return true
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(contentType, "pgp-keys") {
		return true
	}
	name := strings.ToLower(filepath.Base(filename))
	return strings.HasSuffix(name, ".asc") || strings.HasSuffix(name, ".pgp") || strings.HasSuffix(name, ".gpg")
}

func signatureIdentity(text string, result gpgVerifyResult) signatureID {
	id := signatureIDFromStatus(result.status)
	packetID := signatureIDFromPackets(text)
	if id.fingerprint == "" {
		id.fingerprint = packetID.fingerprint
	}
	if id.keyID == "" {
		id.keyID = firstNonEmpty(packetID.keyID, keyIDFromFingerprint(id.fingerprint))
	}
	return id
}

func signatureIDFromStatus(status string) signatureID {
	id := signatureID{}
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "[GNUPG:]" {
			continue
		}
		switch fields[1] {
		case "VALIDSIG":
			if len(fields) > 2 {
				id.fingerprint = normalizeKeyID(fields[2])
			}
		case "NO_PUBKEY", "ERRSIG", "BADSIG", "GOODSIG":
			if len(fields) > 2 && id.keyID == "" {
				id.keyID = normalizeKeyID(fields[2])
			}
		}
	}
	if id.keyID == "" {
		id.keyID = keyIDFromFingerprint(id.fingerprint)
	}
	return id
}

func signatureIDFromPackets(text string) signatureID {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", "--batch", "--list-packets")
	cmd.Stdin = strings.NewReader(text)
	out, _ := cmd.CombinedOutput()
	data := string(out)
	fprRE := regexp.MustCompile(`(?i)issuer fpr[^A-F0-9]*([A-F0-9]{40,64})`)
	keyRE := regexp.MustCompile(`(?i)keyid[ :]+([A-F0-9]{16})`)
	id := signatureID{}
	if match := fprRE.FindStringSubmatch(data); len(match) > 1 {
		id.fingerprint = normalizeKeyID(match[1])
	}
	if match := keyRE.FindStringSubmatch(data); len(match) > 1 {
		id.keyID = normalizeKeyID(match[1])
	}
	if id.keyID == "" {
		id.keyID = keyIDFromFingerprint(id.fingerprint)
	}
	return id
}

func publicKeyIdentities(key []byte) signatureID {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", "--batch", "--with-colons", "--import-options", "show-only", "--import")
	cmd.Stdin = bytes.NewReader(key)
	out, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return signatureID{}
	}
	id := signatureID{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub", "sub":
			if id.keyID == "" {
				id.keyID = normalizeKeyID(fields[4])
			}
		case "fpr":
			if id.fingerprint == "" {
				id.fingerprint = normalizeKeyID(fields[9])
			}
		}
	}
	if id.keyID == "" {
		id.keyID = keyIDFromFingerprint(id.fingerprint)
	}
	return id
}

func localTrustStatus(identity string) string {
	if strings.TrimSpace(identity) == "" {
		return "local trust unknown"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", "--batch", "--with-colons", "--list-keys", identity)
	out, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return "local trust unknown"
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 2 || fields[0] != "pub" {
			continue
		}
		return trustName(fields[1])
	}
	return "local trust unknown"
}

func trustName(validity string) string {
	switch validity {
	case "u":
		return "ultimate trust"
	case "f":
		return "full trust"
	case "m":
		return "marginal trust"
	case "n":
		return "not trusted"
	case "r":
		return "revoked"
	case "e":
		return "expired"
	default:
		return "local trust unknown"
	}
}

func (id signatureID) lookupValues() []string {
	values := []string{}
	if id.fingerprint != "" {
		values = append(values, id.fingerprint)
	}
	if id.keyID != "" && id.keyID != id.fingerprint {
		values = append(values, id.keyID)
	}
	return values
}

func (id signatureID) empty() bool {
	return id.fingerprint == "" && id.keyID == ""
}

func (id signatureID) matches(other signatureID) bool {
	if id.empty() || other.empty() {
		return false
	}
	for _, left := range id.lookupValues() {
		for _, right := range other.lookupValues() {
			if sameKeyID(left, right) {
				return true
			}
		}
	}
	return false
}

func sameKeyID(left, right string) bool {
	left = normalizeKeyID(left)
	right = normalizeKeyID(right)
	return left != "" && right != "" && (left == right || strings.HasSuffix(left, right) || strings.HasSuffix(right, left))
}

func normalizeKeyID(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	clean := strings.Builder{}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') {
			clean.WriteRune(r)
		}
	}
	return clean.String()
}

func keyIDFromFingerprint(fpr string) string {
	fpr = normalizeKeyID(fpr)
	if len(fpr) <= 16 {
		return fpr
	}
	return fpr[len(fpr)-16:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func ApplyDraft(from string, draft protocol.Draft) (protocol.Draft, string, error) {
	options := ParseOptions(draft.PGP)
	encrypt := options["encrypt"] || options["encrypted"]
	sign := options["sign"] || options["signed"]
	selfEncrypt := options["self-encrypt"] || options["selfencrypt"] || options["self"]
	attachPublicKey := options["attach-pubkey"] || options["attach-public-key"] || options["pubkey"]
	if selfEncrypt && !encrypt {
		return draft, "", fmt.Errorf("pgp: self-encrypt needs encrypt")
	}
	needsSecret := sign || selfEncrypt || attachPublicKey
	if needsSecret && !HasSecretKey(from) {
		return draft, "", fmt.Errorf("pgp: no secret key for %s", from)
	}
	status := []string{}
	if attachPublicKey {
		key, err := ExportPublicKey(from)
		if err != nil {
			return draft, "", err
		}
		draft.Attachments = append(draft.Attachments, protocol.Attachment{Filename: publicKeyFilename(from), ContentType: "application/pgp-keys", Data: key})
		status = append(status, "public key attached")
	}
	if !encrypt && !sign {
		draft.PGP = ""
		if len(status) > 0 {
			return draft, "pgp: " + strings.Join(status, "; "), nil
		}
		return draft, "", nil
	}
	args := []string{"--batch", "--yes", "--armor"}
	if sign {
		args = append(args, "--local-user", from)
	}
	if encrypt {
		recipients := draftRecipients(draft)
		if len(recipients) == 0 {
			return draft, "", fmt.Errorf("pgp: encrypt needs recipient")
		}
		missing := MissingPublicKeys(recipients)
		if len(missing) > 0 {
			return draft, "", fmt.Errorf("pgp: missing public key for %s", strings.Join(missing, ", "))
		}
		if selfEncrypt {
			recipients = append(recipients, from)
			status = append(status, "self encrypted")
		}
		args = append(args, "--encrypt")
		for _, recipient := range recipients {
			args = append(args, "--recipient", recipient)
		}
	}
	if sign && encrypt {
		args = append(args, "--sign")
	} else if sign {
		args = append(args, "--clearsign")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", args...)
	cmd.Stdin = strings.NewReader(draft.Body)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return draft, "", fmt.Errorf("pgp: send timed out")
		}
		return draft, "", fmt.Errorf("pgp: send failed: %s", oneLine(stderr.String()))
	}
	draft.Body = strings.TrimSpace(stdout.String())
	draft.PGP = ""
	switch {
	case encrypt && sign:
		status = append([]string{"encrypted", "signed"}, status...)
	case encrypt:
		status = append([]string{"encrypted"}, status...)
	default:
		status = append([]string{"signed"}, status...)
	}
	return draft, "pgp: " + strings.Join(status, "; "), nil
}

func CheckAvailability(from string, draft protocol.Draft) Availability {
	hasSecret := HasSecretKey(from)
	hasSenderPublicKey := HasPublicKey(from)
	recipients := draftRecipients(draft)
	encrypt := len(recipients) > 0 && len(MissingPublicKeys(recipients)) == 0
	return Availability{
		Sign:            hasSecret,
		Encrypt:         encrypt,
		SelfEncrypt:     hasSecret && hasSenderPublicKey && encrypt,
		AttachPublicKey: hasSecret && hasSenderPublicKey,
	}
}

func HasSecretKey(identity string) bool {
	return hasGPGRecord([]string{"sec", "ssb"}, "--list-secret-keys", identity)
}

func HasPublicKey(identity string) bool {
	return hasGPGRecord([]string{"pub", "sub"}, "--list-keys", identity)
}

func MissingPublicKeys(recipients []string) []string {
	missing := []string{}
	seen := map[string]bool{}
	for _, recipient := range recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" || seen[strings.ToLower(recipient)] {
			continue
		}
		seen[strings.ToLower(recipient)] = true
		if !HasPublicKey(recipient) {
			missing = append(missing, recipient)
		}
	}
	return missing
}

func ExportPublicKey(identity string) ([]byte, error) {
	if !HasSecretKey(identity) {
		return nil, fmt.Errorf("pgp: no secret key for %s", identity)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpg", "--batch", "--yes", "--armor", "--export", identity)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("pgp: export timed out")
		}
		return nil, fmt.Errorf("pgp: export failed: %s", oneLine(stderr.String()))
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("pgp: public key not found for %s", identity)
	}
	return out, nil
}

func hasGPGRecord(kinds []string, args ...string) bool {
	if len(args) == 0 || strings.TrimSpace(args[len(args)-1]) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmdArgs := append([]string{"--batch", "--with-colons"}, args...)
	cmd := exec.CommandContext(ctx, "gpg", cmdArgs...)
	out, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return false
	}
	allowed := map[string]bool{}
	for _, kind := range kinds {
		allowed[kind] = true
	}
	for _, line := range strings.Split(string(out), "\n") {
		kind, _, ok := strings.Cut(line, ":")
		if ok && allowed[kind] {
			return true
		}
	}
	return false
}

func pgpStatus(stderr string, encrypted bool) string {
	lower := strings.ToLower(stderr)
	base := "pgp:"
	if encrypted {
		base = "pgp: decrypted"
	}
	if strings.Contains(lower, "good signature") || strings.Contains(lower, "[gnupg:] goodsig") || strings.Contains(lower, "[gnupg:] validsig") {
		if encrypted {
			return base + "; signature valid"
		}
		return base + " signature valid"
	}
	if strings.Contains(lower, "bad signature") || strings.Contains(lower, "[gnupg:] badsig") {
		if encrypted {
			return base + "; signature BAD"
		}
		return base + " signature BAD"
	}
	if strings.Contains(lower, "can't check signature") || strings.Contains(lower, "no public key") || strings.Contains(lower, "[gnupg:] no_pubkey") {
		if encrypted {
			return base + "; signature cannot be verified: No public key"
		}
		return base + " signature cannot be verified: No public key"
	}
	if encrypted {
		return base
	}
	if message := oneLine(stderr); message != "" {
		return fmt.Sprintf("pgp: %s", message)
	}
	return "pgp: signature processed"
}

func oneLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func pgpFailureStatus(action, stderr string, err error) string {
	if status := pgpStatus(stderr, false); status != "" && status != "pgp: signature processed" {
		return status
	}
	if message := oneLine(stderr); message != "" {
		return "pgp: " + action + " failed: " + message
	}
	if err != nil {
		return "pgp: " + action + " failed: " + err.Error()
	}
	return "pgp: " + action + " failed"
}

func isMissingPublicKeyStatus(status, stderr string) bool {
	text := strings.ToLower(status + "\n" + stderr)
	return strings.Contains(text, "no public key") || strings.Contains(text, "can't check signature") || strings.Contains(text, "cannot be verified")
}

func ParseOptions(value string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = true
		}
	}
	return out
}

var filenameClean = regexp.MustCompile(`[^A-Za-z0-9_.@-]+`)

func publicKeyFilename(identity string) string {
	identity = strings.ToLower(strings.TrimSpace(identity))
	if parsed, err := mail.ParseAddress(identity); err == nil {
		identity = strings.ToLower(parsed.Address)
	}
	identity = strings.Trim(filenameClean.ReplaceAllString(identity, "-"), "-.")
	if identity == "" {
		identity = "key"
	}
	return identity + ".asc"
}

func draftRecipients(draft protocol.Draft) []string {
	value := strings.Join([]string{draft.To, draft.Cc, draft.Bcc}, ",")
	parsed, err := mail.ParseAddressList(value)
	if err == nil {
		out := make([]string, 0, len(parsed))
		for _, address := range parsed {
			out = append(out, address.Address)
		}
		return out
	}
	out := []string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
