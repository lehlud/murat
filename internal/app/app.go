package app

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lehnert.dev/murat/internal/compose"
	"lehnert.dev/murat/internal/lsp"
	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/protocol"
	"lehnert.dev/murat/internal/store"
	"lehnert.dev/murat/internal/ui"
)

var commit = "dev"

func Main(args []string) error {
	if len(args) == 0 {
		args = []string{"tui"}
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "account":
		return cmdAccount(args[1:])
	case "paths":
		return cmdPaths()
	case "import-eml":
		return cmdImport(args[1:])
	case "list":
		return cmdList(args[1:])
	case "open":
		return cmdOpen(args[1:])
	case "save-attachments":
		return cmdSaveAttachments(args[1:])
	case "read":
		return cmdRead(args[1:], true)
	case "unread":
		return cmdRead(args[1:], false)
	case "tui":
		return cmdTUI(args[1:])
	case "compose":
		return cmdCompose(args[1:])
	case "sync":
		return cmdSync(args[1:])
	case "lsp":
		return cmdLSP()
	case "version":
		fmt.Println(commit)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Println("murat (go) " + commit)
	fmt.Println("commands: init, account, sync, compose, paths, import-eml, list, open, save-attachments, read, unread, tui, lsp, version")
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	gpg := fs.String("gpg-key", "", "GPG recipient used to wrap local key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths := store.DefaultPaths()
	if _, err := store.EnsureKey(paths, *gpg); err != nil {
		return err
	}
	fmt.Println("initialized")
	return nil
}

func cmdPaths() error {
	paths := store.DefaultPaths()
	fmt.Printf("config=%s\n", paths.ConfigDir)
	fmt.Printf("config_file=%s\n", paths.ConfigFile)
	fmt.Printf("data=%s\n", paths.DataDir)
	fmt.Printf("key=%s\n", paths.KeyFile)
	fmt.Printf("mail=%s\n", paths.IndexFile)
	fmt.Printf("accounts=%s\n", paths.AccountsFile)
	fmt.Printf("search=%s\n", paths.SearchFile)
	fmt.Printf("eml=%s\n", paths.RawDir)
	return nil
}

func cmdAccount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: murat account list|add-imap|add-jmap|remove")
	}
	accounts, err := openAccounts()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		items, err := accounts.All()
		if err != nil {
			return err
		}
		for _, account := range items {
			fmt.Printf("%s\t%s\t%s\n", account.ID, account.Email, account.Protocol)
		}
		return nil
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: murat account remove ID")
		}
		return accounts.Remove(args[1])
	case "add-imap":
		fs := flag.NewFlagSet("account add-imap", flag.ContinueOnError)
		email := fs.String("email", "", "email address")
		name := fs.String("name", "", "display name")
		imapHost := fs.String("imap-host", "", "IMAPS host")
		imapPort := fs.Int("imap-port", 993, "IMAPS port")
		mailbox := fs.String("mailbox", "INBOX", "IMAP mailbox")
		smtpHost := fs.String("smtp-host", "", "SMTPS host")
		smtpPort := fs.Int("smtp-port", 465, "SMTPS port")
		username := fs.String("username", "", "IMAP/SMTP username")
		password := fs.String("password", "", "IMAP/SMTP password")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		reader := bufio.NewReader(os.Stdin)
		var err error
		*email, err = promptValue(reader, "email", *email, "", true)
		if err != nil {
			return err
		}
		*name, err = promptValue(reader, "name", *name, "", false)
		if err != nil {
			return err
		}
		domain := emailDomain(*email)
		*imapHost, err = promptValue(reader, "IMAPS host", *imapHost, hostDefault("imap", domain), true)
		if err != nil {
			return err
		}
		*mailbox, err = promptValue(reader, "IMAP mailbox", *mailbox, "INBOX", true)
		if err != nil {
			return err
		}
		*smtpHost, err = promptValue(reader, "SMTPS host", *smtpHost, hostDefault("smtp", domain), true)
		if err != nil {
			return err
		}
		*username, err = promptValue(reader, "username", *username, *email, true)
		if err != nil {
			return err
		}
		*password, err = promptSecret(reader, "password", *password)
		if err != nil {
			return err
		}
		user := *username
		if user == "" {
			user = *email
		}
		id := store.StableAccountID(*email, *imapHost)
		return accounts.Upsert(store.Account{ID: id, Name: *name, Email: *email, Protocol: "imap", Username: user, Secret: *password, IMAPHost: *imapHost, IMAPPort: *imapPort, IMAPMailbox: *mailbox, SMTPHost: *smtpHost, SMTPPort: *smtpPort, SMTPUsername: user, SMTPSecret: *password})
	case "add-jmap":
		fs := flag.NewFlagSet("account add-jmap", flag.ContinueOnError)
		email := fs.String("email", "", "email address")
		name := fs.String("name", "", "display name")
		sessionURL := fs.String("session-url", "", "JMAP session URL")
		authKind := fs.String("auth", "bearer", "bearer or basic")
		username := fs.String("username", "", "basic auth username")
		secret := fs.String("secret", "", "token/password")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		reader := bufio.NewReader(os.Stdin)
		var err error
		*email, err = promptValue(reader, "email", *email, "", true)
		if err != nil {
			return err
		}
		*name, err = promptValue(reader, "name", *name, "", false)
		if err != nil {
			return err
		}
		sessionDefault := discoverJMAPSession(emailDomain(*email))
		*sessionURL, err = promptValue(reader, "JMAP session URL", *sessionURL, sessionDefault, true)
		if err != nil {
			return err
		}
		*authKind, err = promptValue(reader, "auth (bearer/basic)", *authKind, "bearer", true)
		if err != nil {
			return err
		}
		if strings.EqualFold(*authKind, "basic") {
			*username, err = promptValue(reader, "username", *username, *email, true)
			if err != nil {
				return err
			}
		}
		*secret, err = promptSecret(reader, "token/password", *secret)
		if err != nil {
			return err
		}
		id := store.StableAccountID(*email, *sessionURL)
		return accounts.Upsert(store.Account{ID: id, Name: *name, Email: *email, Protocol: "jmap", SessionURL: *sessionURL, AuthKind: *authKind, Username: *username, Secret: *secret})
	default:
		return fmt.Errorf("unknown account command %q", args[0])
	}
}

func cmdImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: murat import-eml FILE...")
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	count := 0
	for _, path := range args {
		err := filepath.WalkDir(path, func(item string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".eml") {
				return nil
			}
			data, err := os.ReadFile(item)
			if err != nil {
				return err
			}
			msg, err := s.ImportRaw(data)
			if err != nil {
				return err
			}
			count++
			fmt.Printf("imported %s\n", msg.Key[:10])
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := s.Flush(); err != nil {
		return err
	}
	fmt.Printf("imported %d messages\n", count)
	return nil
}

func cmdList(args []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	for _, msg := range s.Messages(false) {
		marker := " "
		if !msg.Read {
			marker = "*"
		}
		fmt.Printf("%s %.10s %-25s %s\n", marker, msg.Key, trim(msg.From, 25), msg.Subject)
	}
	return nil
}

func cmdOpen(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: murat open MESSAGE")
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	msg, err := resolveMessage(s, args[0])
	if err != nil {
		return err
	}
	msg.SetRead(true)
	body, err := s.OpenBody(msg)
	if err != nil {
		return err
	}
	text, status, _ := pgp.ProcessText(body.Text)
	fmt.Println(body.Headers)
	fmt.Println()
	fmt.Println(text)
	if status != "" {
		fmt.Fprintln(os.Stderr, status)
	}
	return s.Flush()
}

func cmdRead(args []string, read bool) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: murat read|unread MESSAGE")
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	msg, err := resolveMessage(s, args[0])
	if err != nil {
		return err
	}
	msg.SetRead(read)
	return s.Flush()
}

func cmdSaveAttachments(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: murat save-attachments MESSAGE [DIR]")
	}
	dir := "."
	if len(args) == 2 {
		dir = args[1]
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	msg, err := resolveMessage(s, args[0])
	if err != nil {
		return err
	}
	paths, err := s.SaveAttachments(msg, dir)
	if err != nil {
		return err
	}
	for _, path := range paths {
		fmt.Println(path)
	}
	fmt.Printf("saved %d attachments\n", len(paths))
	return nil
}

func cmdTUI(args []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	accounts, err := openAccounts()
	if err != nil {
		return err
	}
	return ui.Run(s, accounts)
}

func cmdLSP() error {
	s, err := openStore()
	if err != nil {
		return err
	}
	return lsp.Run(os.Stdin, os.Stdout, s.KnownAddresses())
}

func cmdCompose(args []string) error {
	fs := flag.NewFlagSet("compose", flag.ContinueOnError)
	accountID := fs.String("account", "", "account id/email")
	from := fs.String("from", "", "sender address/account")
	to := fs.String("to", "", "recipient")
	cc := fs.String("cc", "", "cc")
	bcc := fs.String("bcc", "", "bcc")
	subject := fs.String("subject", "", "subject")
	pgpOpt := fs.String("pgp", "", "PGP options: encrypt, sign, or encrypt,sign")
	bodyFile := fs.String("body-file", "", "body file; stdin if empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	accounts, err := openAccounts()
	if err != nil {
		return err
	}
	account, err := accounts.Get(*accountID)
	if err != nil {
		return err
	}
	draft := protocol.Draft{From: *from, To: *to, Cc: *cc, Bcc: *bcc, Subject: *subject, PGP: *pgpOpt}
	if strings.TrimSpace(draft.From) == "" {
		draft.From = account.Email
	}
	if *bodyFile != "" {
		body, err := os.ReadFile(*bodyFile)
		if err != nil {
			return err
		}
		draft.Body = string(body)
	} else if stdinIsTerminal() {
		draft, err = compose.Edit(draft)
		if err != nil {
			return err
		}
	} else {
		body, err := os.ReadFile("/dev/stdin")
		if err != nil {
			return err
		}
		draft.Body = string(body)
	}
	if compose.EmptyRecipient(draft) {
		return fmt.Errorf("add at least one recipient")
	}
	account, err = sendAccountForDraft(accounts, account, &draft)
	if err != nil {
		return err
	}
	draft, status, err := pgp.ApplyDraft(senderEmail(draft.From), draft)
	if err != nil {
		return err
	}
	if account.Protocol == "jmap" {
		err = protocol.SendJMAP(account, draft)
	} else {
		err = protocol.SendSMTPS(account, draft)
	}
	if err != nil {
		return err
	}
	if status != "" {
		fmt.Fprintln(os.Stderr, status)
	}
	if s, err := openStore(); err == nil {
		s.RememberAddressStrings(draft.From, draft.To, draft.Cc, draft.Bcc)
		_ = s.Flush()
	}
	return nil
}

func sendAccountForDraft(accounts *store.AccountStore, fallback store.Account, draft *protocol.Draft) (store.Account, error) {
	if strings.TrimSpace(draft.From) == "" {
		draft.From = fallback.Email
		return fallback, nil
	}
	email := senderEmail(draft.From)
	if email == "" {
		return store.Account{}, fmt.Errorf("from address required")
	}
	if strings.EqualFold(email, fallback.Email) {
		return fallback, nil
	}
	account, err := accounts.Get(email)
	if err != nil {
		return store.Account{}, err
	}
	if !strings.Contains(email, "@") {
		draft.From = account.Email
	}
	return account, nil
}

func senderEmail(value string) string {
	value = strings.TrimSpace(value)
	if addr, err := mail.ParseAddress(value); err == nil {
		return addr.Address
	}
	return value
}

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	accountID := fs.String("account", "", "account id/email")
	limit := fs.Int("limit", 100, "max messages per account; 0 = all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	accounts, err := openAccounts()
	if err != nil {
		return err
	}
	items, err := accounts.All()
	if err != nil {
		return err
	}
	if *accountID != "" {
		account, err := accounts.Get(*accountID)
		if err != nil {
			return err
		}
		items = []store.Account{account}
	}
	total := 0
	for _, account := range items {
		switch account.Protocol {
		case "imap", "imaps":
			count, err := protocol.SyncIMAPS(account, s, *limit, func(message string) { fmt.Fprintf(os.Stderr, "%s: %s\n", account.Email, message) })
			if err != nil {
				return err
			}
			fmt.Printf("%s: %d messages\n", account.Email, count)
			total += count
		case "jmap":
			count, err := protocol.SyncJMAP(account, s, *limit, nil)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %d messages\n", account.Email, count)
			total += count
		}
	}
	fmt.Printf("synced %d messages\n", total)
	return nil
}

func openStore() (*store.Store, error) {
	paths := store.DefaultPaths()
	key, err := store.LoadKey(paths)
	if err != nil {
		return nil, fmt.Errorf("not initialized: %w", err)
	}
	return store.Open(paths, key)
}

func openAccounts() (*store.AccountStore, error) {
	paths := store.DefaultPaths()
	key, err := store.LoadKey(paths)
	if err != nil {
		return nil, fmt.Errorf("not initialized: %w", err)
	}
	return store.NewAccountStore(paths, key)
}

func resolveMessage(s *store.Store, prefix string) (*store.Message, error) {
	if msg, ok := s.Message(prefix); ok {
		return msg, nil
	}
	matches := []*store.Message{}
	for _, msg := range s.MessagesAll(true, true) {
		if strings.HasPrefix(msg.Key, prefix) {
			matches = append(matches, msg)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous message prefix")
	}
	return nil, fmt.Errorf("message not found")
}

func promptValue(reader *bufio.Reader, label, current, fallback string, required bool) (string, error) {
	if strings.TrimSpace(current) != "" {
		return current, nil
	}
	prompt := label
	if fallback != "" {
		prompt += " [" + fallback + "]"
	}
	fmt.Print(prompt + ": ")
	value, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if required && value == "" {
		return "", fmt.Errorf("%s required", label)
	}
	return value, nil
}

func promptSecret(reader *bufio.Reader, label, current string) (string, error) {
	if current != "" {
		return current, nil
	}
	fmt.Print(label + ": ")
	setEcho(false)
	value, err := reader.ReadString('\n')
	setEcho(true)
	fmt.Println()
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s required", label)
	}
	return value, nil
}

func setEcho(enabled bool) {
	arg := "echo"
	if !enabled {
		arg = "-echo"
	}
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}

func emailDomain(email string) string {
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return ""
	}
	return strings.TrimSpace(domain)
}

func hostDefault(prefix, domain string) string {
	if domain == "" {
		return ""
	}
	return prefix + "." + domain
}

func discoverJMAPSession(domain string) string {
	if domain == "" {
		return ""
	}
	_, records, err := net.LookupSRV("jmap", "tcp", domain)
	if err == nil && len(records) > 0 {
		target := strings.TrimSuffix(records[0].Target, ".")
		if records[0].Port == 443 {
			return "https://" + target + "/.well-known/jmap"
		}
		return fmt.Sprintf("https://%s:%d/.well-known/jmap", target, records[0].Port)
	}
	return "https://" + domain + "/.well-known/jmap"
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func trim(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}
