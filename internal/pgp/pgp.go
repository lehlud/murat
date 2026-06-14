package pgp

import (
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"os/exec"
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
	encrypted := strings.Contains(text, "-----BEGIN PGP MESSAGE-----")
	signed := strings.Contains(text, "-----BEGIN PGP SIGNED MESSAGE-----")
	if !encrypted && !signed {
		return text, "", false
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
	if strings.Contains(lower, "good signature") {
		if encrypted {
			return base + "; signature valid"
		}
		return base + " signature valid"
	}
	if strings.Contains(lower, "bad signature") {
		if encrypted {
			return base + "; signature BAD"
		}
		return base + " signature BAD"
	}
	if strings.Contains(lower, "can't check signature") || strings.Contains(lower, "no public key") {
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
