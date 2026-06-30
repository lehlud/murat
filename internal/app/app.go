package app

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lehnert.dev/murat/internal/compose"
	"lehnert.dev/murat/internal/config"
	"lehnert.dev/murat/internal/lsp"
	"lehnert.dev/murat/internal/oauth"
	"lehnert.dev/murat/internal/pgp"
	"lehnert.dev/murat/internal/protocol"
	"lehnert.dev/murat/internal/store"
	"lehnert.dev/murat/internal/ui"
	"lehnert.dev/murat/internal/userdirs"
)

var commit = "dev"

const defaultSyncLimit = 350

func Main(args []string) error {
	pgp.ActivateManagedHomeIfPresent()
	if len(args) == 0 {
		args = []string{"tui"}
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "account":
		return cmdAccount(args[1:])
	case "export":
		return cmdExport(args[1:])
	case "import":
		return cmdImportArchive(args[1:])
	case "paths":
		if helpRequested(args[1:]) {
			usageSimple("paths", "print config/data paths")
			return nil
		}
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
		if helpRequested(args[1:]) {
			usageSimple("lsp", "serve completion LSP on stdio")
			return nil
		}
		return cmdLSP()
	case "version":
		if helpRequested(args[1:]) {
			usageSimple("version", "print version")
			return nil
		}
		fmt.Println(commit)
		return nil
	case "help":
		return cmdHelp(args[1:])
	case "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Println(helpTitle("murat") + " " + commit)
	fmt.Println()
	fmt.Println(helpSection("Usage:"))
	fmt.Println("  " + helpCommand("murat [command] [flags]"))
	fmt.Println()
	fmt.Println(helpSection("Commands:"))
	usageCommand("init", "initialize encrypted local store")
	usageCommand("account", "manage mail accounts")
	usageCommand("export", "export encrypted account and GPG backup")
	usageCommand("import", "import encrypted account and GPG backup")
	usageCommand("sync", "fetch new mail")
	usageCommand("compose", "compose and send mail")
	usageCommand("tui", "open fullscreen UI")
	usageCommand("list", "list local messages")
	usageCommand("open", "print one message")
	usageCommand("save-attachments", "save message attachments")
	usageCommand("read", "mark message read")
	usageCommand("unread", "mark message unread")
	usageCommand("import-eml", "import .eml files")
	usageCommand("paths", "print config/data paths")
	usageCommand("lsp", "serve completion LSP")
	usageCommand("version", "print version")
	fmt.Println()
	fmt.Println("Use \"murat COMMAND --help\" for command help.")
}

func cmdHelp(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "account":
		if len(args) > 1 {
			return cmdAccount(append(args[1:], "--help"))
		}
		usageAccount()
	case "init":
		usageInit(nil)
	case "sync":
		usageSync(nil)
	case "compose":
		usageCompose(nil)
	case "export":
		usageExport(nil)
	case "import":
		usageImport(nil)
	case "paths":
		usageSimple("paths", "print config/data paths")
	case "import-eml":
		usageSimple("import-eml FILE...", "import .eml files from files or directories")
	case "list":
		usageSimple("list", "list local messages")
	case "open":
		usageSimple("open MESSAGE", "print one message and mark it read")
	case "save-attachments":
		usageSimple("save-attachments MESSAGE [DIR]", "save message attachments")
	case "read":
		usageSimple("read MESSAGE", "mark message read")
	case "unread":
		usageSimple("unread MESSAGE", "mark message unread")
	case "tui":
		usageSimple("tui", "open fullscreen UI")
	case "lsp":
		usageSimple("lsp", "serve completion LSP on stdio")
	case "version":
		usageSimple("version", "print version")
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

func commandFlagSet(name string, usageFn func(*flag.FlagSet)) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() { usageFn(fs) }
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) (bool, error) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func helpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			return true
		}
	}
	return false
}

func usageSimple(command, summary string) {
	fmt.Println(helpSection("Usage:"))
	fmt.Println("  " + helpCommand("murat "+command))
	if summary != "" {
		fmt.Println()
		fmt.Println(summary)
	}
}

func usageFlags(command, summary string, fs *flag.FlagSet) {
	usageSimple(command, summary)
	hasFlags := false
	if fs != nil {
		fs.VisitAll(func(*flag.Flag) { hasFlags = true })
	}
	if hasFlags {
		fmt.Println()
		fmt.Println(helpSection("Flags:"))
		printFlagDefaults(fs)
	}
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(item *flag.Flag) {
		if item.Name == name {
			found = true
		}
	})
	return found
}

func usageCommand(name, summary string) {
	padding := 22 - len(name)
	if padding < 1 {
		padding = 1
	}
	fmt.Printf("  %s%s%s\n", helpCommand(name), strings.Repeat(" ", padding), summary)
}

func printFlagDefaults(fs *flag.FlagSet) {
	fs.VisitAll(func(item *flag.Flag) {
		name, usage := flag.UnquoteUsage(item)
		line := "-" + item.Name
		if name != "" {
			line += " " + name
		}
		fmt.Println("  " + helpFlag(line))
		if defaultValue := helpDefaultValue(item.DefValue); defaultValue != "" {
			usage += " (default " + defaultValue + ")"
		}
		fmt.Println("\t" + usage)
	})
}

func helpDefaultValue(value string) string {
	if value == "" || value == "0" || value == "false" {
		return ""
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return value
	}
	return fmt.Sprintf("%q", value)
}

func helpTitle(text string) string   { return helpStyle("1;36", text) }
func helpSection(text string) string { return helpStyle("1", text) }
func helpCommand(text string) string { return helpStyle("36", text) }
func helpFlag(text string) string    { return helpStyle("33", text) }

func helpStyle(code, text string) string {
	if !helpColorEnabled() {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func helpColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	info, err := os.Stdout.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func cmdInit(args []string) error {
	fs := commandFlagSet("init", usageInit)
	gpg := fs.String("gpg-key", "", "GPG recipient used to wrap local key")
	if handled, err := parseFlags(fs, args); handled || err != nil {
		return err
	}
	paths := store.DefaultPaths()
	if strings.TrimSpace(*gpg) == "" {
		cfg, err := config.Load(paths.ConfigFile)
		if err != nil {
			return err
		}
		*gpg = cfg.Crypto.GPGRecipient
	}
	if _, err := store.EnsureKey(paths, *gpg); err != nil {
		return err
	}
	fmt.Println("initialized")
	return nil
}

func usageInit(fs *flag.FlagSet) {
	usageFlags("init [flags]", "initialize encrypted local store", fs)
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
		usageAccount()
		return fmt.Errorf("missing account command")
	}
	if args[0] == "help" {
		if len(args) > 1 {
			return cmdAccount(append(args[1:], "--help"))
		}
		usageAccount()
		return nil
	}
	if helpRequested(args[:1]) {
		usageAccount()
		return nil
	}
	switch args[0] {
	case "list":
		if helpRequested(args[1:]) {
			usageAccountList()
			return nil
		}
		accounts, err := openAccounts()
		if err != nil {
			return err
		}
		items, err := accounts.All()
		if err != nil {
			return err
		}
		for _, account := range items {
			fmt.Printf("%s\t%s\t%s\n", account.ID, account.Email, account.Protocol)
		}
		return nil
	case "remove":
		if helpRequested(args[1:]) {
			usageAccountRemove()
			return nil
		}
		if len(args) != 2 {
			usageAccountRemove()
			return fmt.Errorf("account id required")
		}
		accounts, err := openAccounts()
		if err != nil {
			return err
		}
		return accounts.Remove(args[1])
	case "add-imap":
		fs := commandFlagSet("account add-imap", usageAccountAddIMAP)
		email := fs.String("email", "", "email address")
		name := fs.String("name", "", "display name")
		imapHost := fs.String("imap-host", "", "IMAPS host")
		imapPort := fs.Int("imap-port", 993, "IMAPS port")
		mailbox := fs.String("mailbox", "INBOX", "IMAP mailbox")
		smtpHost := fs.String("smtp-host", "", "SMTPS host")
		smtpPort := fs.Int("smtp-port", 465, "SMTPS port")
		username := fs.String("username", "", "IMAP/SMTP username")
		password := fs.String("password", "", "IMAP/SMTP password")
		if handled, err := parseFlags(fs, args[1:]); handled || err != nil {
			return err
		}
		accounts, err := openAccounts()
		if err != nil {
			return err
		}
		promptIntro("Add IMAP Account", "Password auth with IMAPS sync and SMTPS send.")
		reader := bufio.NewReader(os.Stdin)
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
		*username, err = promptValue(reader, "username", *username, *email, true)
		if err != nil {
			return err
		}
		user := *username
		if user == "" {
			user = *email
		}
		id := store.StableAccountID(*email, *imapHost)
		*smtpHost, err = promptValue(reader, "SMTPS host", *smtpHost, hostDefault("smtp", domain), true)
		if err != nil {
			return err
		}
		*password, err = promptSecret(reader, "password", *password)
		if err != nil {
			return err
		}
		return saveAccount(accounts, store.Account{ID: id, Name: *name, Email: *email, Protocol: "imap", Username: user, Secret: *password, IMAPHost: *imapHost, IMAPPort: *imapPort, IMAPMailbox: *mailbox, SMTPHost: *smtpHost, SMTPPort: *smtpPort, SMTPUsername: user, SMTPSecret: *password})
	case "add-exchange-online":
		fs := commandFlagSet("account add-exchange-online", usageAccountAddExchangeOnline)
		email := fs.String("email", "", "email address")
		name := fs.String("name", "", "display name")
		imapHost := fs.String("imap-host", "outlook.office365.com", "IMAPS host")
		imapPort := fs.Int("imap-port", 993, "IMAPS port")
		mailbox := fs.String("mailbox", "INBOX", "IMAP mailbox")
		smtpHost := fs.String("smtp-host", "smtp.office365.com", "SMTP host")
		smtpPort := fs.Int("smtp-port", 587, "SMTP STARTTLS port")
		username := fs.String("username", "", "mailbox username")
		oauthTenant := fs.String("oauth-tenant", "", "Microsoft OAuth tenant id/domain; common only for multi-tenant apps")
		oauthClientID := fs.String("oauth-client-id", "", "Microsoft OAuth client id")
		oauthScopes := fs.String("oauth-scopes", oauth.ScopeString(oauth.DefaultMicrosoftMailScopes()), "OAuth scopes")
		if handled, err := parseFlags(fs, args[1:]); handled || err != nil {
			return err
		}
		oauthTenantSet := flagWasSet(fs, "oauth-tenant")
		accounts, err := openAccounts()
		if err != nil {
			return err
		}
		promptIntro("Add Exchange Online Account", "Microsoft OAuth2 device login with IMAP sync and SMTP send.")
		reader := bufio.NewReader(os.Stdin)
		*email, err = promptValue(reader, "email", *email, "", true)
		if err != nil {
			return err
		}
		*name, err = promptValue(reader, "name", *name, "", false)
		if err != nil {
			return err
		}
		*username, err = promptValue(reader, "username", *username, *email, true)
		if err != nil {
			return err
		}
		tenantCurrent := *oauthTenant
		if !oauthTenantSet {
			tenantCurrent = ""
		}
		*oauthTenant, err = promptValue(reader, "OAuth tenant", tenantCurrent, oauthTenantDefault(*email), true)
		if err != nil {
			return err
		}
		if strings.TrimSpace(*oauthClientID) == "" {
			printMicrosoftClientIDHelp()
		}
		*oauthClientID, err = promptValue(reader, "OAuth client ID", *oauthClientID, "", true)
		if err != nil {
			return err
		}
		scopes := oauth.ParseScopes(*oauthScopes)
		if len(scopes) == 0 {
			scopes = oauth.DefaultMicrosoftMailScopes()
		}
		token, err := microsoftDeviceLogin(*oauthTenant, *oauthClientID, scopes)
		if err != nil {
			return err
		}
		id := store.StableAccountID(*email, *imapHost)
		return saveAccount(accounts, store.Account{ID: id, Name: *name, Email: *email, Protocol: "exchange-online", Username: *username, Secret: token.RefreshToken, AuthKind: "xoauth2", IMAPHost: *imapHost, IMAPPort: *imapPort, IMAPMailbox: *mailbox, SMTPHost: *smtpHost, SMTPPort: *smtpPort, SMTPUsername: *username, OAuthProvider: "microsoft", OAuthTenant: *oauthTenant, OAuthClientID: *oauthClientID, OAuthScopes: scopes})
	case "add-jmap":
		fs := commandFlagSet("account add-jmap", usageAccountAddJMAP)
		email := fs.String("email", "", "email address")
		name := fs.String("name", "", "display name")
		sessionURL := fs.String("session-url", "", "JMAP session URL")
		authKind := fs.String("auth", "bearer", "bearer or basic")
		username := fs.String("username", "", "basic auth username")
		secret := fs.String("secret", "", "token/password")
		if handled, err := parseFlags(fs, args[1:]); handled || err != nil {
			return err
		}
		accounts, err := openAccounts()
		if err != nil {
			return err
		}
		promptIntro("Add JMAP Account", "Bearer or basic auth for JMAP sync and send.")
		reader := bufio.NewReader(os.Stdin)
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
		return saveAccount(accounts, store.Account{ID: id, Name: *name, Email: *email, Protocol: "jmap", SessionURL: *sessionURL, AuthKind: *authKind, Username: *username, Secret: *secret})
	default:
		usageAccount()
		return fmt.Errorf("unknown account command %q", args[0])
	}
}

func usageAccount() {
	fmt.Println(helpSection("Usage:"))
	fmt.Println("  " + helpCommand("murat account <command> [flags]"))
	fmt.Println()
	fmt.Println(helpSection("Commands:"))
	usageCommand("list", "list accounts")
	usageCommand("add-imap", "add password IMAP/SMTP account")
	usageCommand("add-exchange-online", "add Microsoft 365 account via OAuth2")
	usageCommand("add-jmap", "add JMAP account")
	usageCommand("remove", "remove account")
	fmt.Println()
	fmt.Println("Use \"murat account COMMAND --help\" for command help.")
}

func usageAccountList() {
	usageSimple("account list", "list configured accounts")
}

func usageAccountRemove() {
	usageSimple("account remove ID", "remove account by id or email")
}

func usageAccountAddIMAP(fs *flag.FlagSet) {
	usageFlags("account add-imap [flags]", "add password IMAP/SMTP account", fs)
}

func usageAccountAddExchangeOnline(fs *flag.FlagSet) {
	usageFlags("account add-exchange-online [flags]", "add Exchange Online account via Microsoft OAuth2 device code", fs)
}

func usageAccountAddJMAP(fs *flag.FlagSet) {
	usageFlags("account add-jmap [flags]", "add JMAP account", fs)
}

func cmdImport(args []string) error {
	if helpRequested(args) {
		usageSimple("import-eml FILE...", "import .eml files from files or directories")
		return nil
	}
	if len(args) == 0 {
		usageSimple("import-eml FILE...", "import .eml files from files or directories")
		return fmt.Errorf("file required")
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
	if helpRequested(args) {
		usageSimple("list", "list local messages")
		return nil
	}
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
	if helpRequested(args) {
		usageSimple("open MESSAGE", "print one message and mark it read")
		return nil
	}
	if len(args) != 1 {
		usageSimple("open MESSAGE", "print one message and mark it read")
		return fmt.Errorf("message required")
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
	attachments, _ := s.Attachments(msg)
	text, status, _ := pgp.ProcessTextWithKeys(body.Text, publicKeyAttachmentData(attachments))
	fmt.Println(body.Headers)
	fmt.Println()
	fmt.Println(text)
	if status != "" {
		fmt.Fprintln(os.Stderr, status)
	}
	return s.Flush()
}

func publicKeyAttachmentData(attachments []store.Attachment) [][]byte {
	keys := [][]byte{}
	for _, attachment := range attachments {
		if pgp.IsPublicKeyAttachment(attachment.Filename, attachment.ContentType, attachment.Data) {
			keys = append(keys, attachment.Data)
		}
	}
	return keys
}

func cmdRead(args []string, read bool) error {
	if helpRequested(args) {
		if read {
			usageSimple("read MESSAGE", "mark message read")
		} else {
			usageSimple("unread MESSAGE", "mark message unread")
		}
		return nil
	}
	if len(args) != 1 {
		if read {
			usageSimple("read MESSAGE", "mark message read")
		} else {
			usageSimple("unread MESSAGE", "mark message unread")
		}
		return fmt.Errorf("message required")
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
	if helpRequested(args) {
		usageSimple("save-attachments MESSAGE [DIR]", "save message attachments; DIR defaults to Downloads")
		return nil
	}
	if len(args) < 1 || len(args) > 2 {
		usageSimple("save-attachments MESSAGE [DIR]", "save message attachments; DIR defaults to Downloads")
		return fmt.Errorf("message required")
	}
	dir := userdirs.Downloads()
	if len(args) == 2 {
		dir = args[1]
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
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
	if helpRequested(args) {
		usageSimple("tui", "open fullscreen UI")
		return nil
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	cfg, err := config.Load(s.Paths().ConfigFile)
	if err != nil {
		return err
	}
	accounts, err := openAccounts()
	if err != nil {
		return err
	}
	return ui.Run(s, accounts, ui.Options{
		PGPDefaults: cfg.PGPOptions(),
		PGPIdentity: cfg.Crypto.GPGRecipient,
		Theme:       ui.ThemeFromConfig(cfg.Theme),
		Keys:        ui.KeysFromConfig(cfg.Keys),
		Editor:      cfg.UI.Editor,
		Split:       cfg.UI.Split,
		PageSize:    cfg.UI.PageSize,
	})
}

func cmdLSP() error {
	s, err := openStore()
	if err != nil {
		return err
	}
	return lsp.Run(os.Stdin, os.Stdout, s.KnownAddresses())
}

func cmdCompose(args []string) error {
	fs := commandFlagSet("compose", usageCompose)
	accountID := fs.String("account", "", "account id/email")
	from := fs.String("from", "", "sender address/account")
	to := fs.String("to", "", "recipient")
	cc := fs.String("cc", "", "cc")
	bcc := fs.String("bcc", "", "bcc")
	subject := fs.String("subject", "", "subject")
	pgpOpt := fs.String("pgp", "", "PGP options: encrypt, sign, or encrypt,sign")
	bodyFile := fs.String("body-file", "", "body file; stdin if empty")
	if handled, err := parseFlags(fs, args); handled || err != nil {
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
	cfg, err := config.Load(store.DefaultPaths().ConfigFile)
	if err != nil {
		return err
	}
	if strings.TrimSpace(draft.PGP) == "" {
		draft.PGP = cfg.PGPOptions()
	}
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
		draft, err = compose.EditWithEditor(draft, cfg.UI.Editor)
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
	draft, status, err := pgp.ApplyDraft(pgpIdentity(senderEmail(draft.From), cfg.Crypto.GPGRecipient), draft)
	if err != nil {
		return err
	}
	if account.Protocol == "jmap" {
		err = protocol.SendJMAP(account, draft)
	} else {
		err = protocol.SendSMTPSWithUpdater(account, draft, accounts.Upsert)
	}
	if err != nil {
		return err
	}
	if status != "" {
		fmt.Fprintln(os.Stderr, status)
	}
	if s, err := openStore(); err == nil {
		_, _ = s.ImportSent(account.ID, []byte(protocol.Message(account, draft)))
		s.RememberAddressStrings(draft.From, draft.To, draft.Cc, draft.Bcc)
		_ = s.Flush()
	}
	return nil
}

func usageCompose(fs *flag.FlagSet) {
	usageFlags("compose [flags]", "compose and send mail", fs)
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

func pgpIdentity(sender, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" && (sender == "" || !pgp.HasSecretKey(sender)) {
		return configured
	}
	return sender
}

func cmdSync(args []string) error {
	if helpRequested(args) {
		fs := commandFlagSet("sync", usageSync)
		fs.String("account", "", "account id/email")
		fs.Int("limit", defaultSyncLimit, "max messages per account; 0 = all")
		fs.Usage()
		return nil
	}
	cfg, err := config.Load(store.DefaultPaths().ConfigFile)
	if err != nil {
		return err
	}
	defaultLimit := defaultSyncLimit
	if cfg.UI.PageSize > 0 {
		defaultLimit = cfg.UI.PageSize
	}
	fs := commandFlagSet("sync", usageSync)
	accountID := fs.String("account", "", "account id/email")
	limit := fs.Int("limit", defaultLimit, "max messages per account; 0 = all")
	if handled, err := parseFlags(fs, args); handled || err != nil {
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
		case "imap", "imaps", "exchange-online":
			count, err := protocol.SyncIMAPSWithUpdater(account, s, *limit, func(message string) { fmt.Fprintf(os.Stderr, "%s: %s\n", account.Email, message) }, accounts.Upsert)
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

func usageSync(fs *flag.FlagSet) {
	usageFlags("sync [flags]", "fetch new mail", fs)
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

func saveAccount(accounts *store.AccountStore, account store.Account) error {
	if err := accounts.Upsert(account); err != nil {
		return err
	}
	if stdinIsTerminal() {
		fmt.Printf("%s saved %s (%s)\n", helpStyle("32", "+"), helpStyle("1", account.Email), account.Protocol)
	}
	return nil
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
	fmt.Print(promptQuestion(label, fallback, required))
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
	fmt.Print(promptQuestion(label, "", true))
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

func promptIntro(title, summary string) {
	if !stdinIsTerminal() {
		return
	}
	fmt.Println()
	fmt.Println(helpTitle(title))
	if summary != "" {
		fmt.Println(helpStyle("2", summary))
	}
	fmt.Println(helpStyle("2", "Press Enter to accept defaults."))
	fmt.Println()
}

func promptQuestion(label, fallback string, required bool) string {
	req := helpStyle("2", " optional")
	if required {
		req = helpStyle("31", " required")
	}
	def := ""
	if fallback != "" {
		def = " " + helpStyle("2", "["+fallback+"]")
	}
	return fmt.Sprintf("%s %s%s%s: ", helpStyle("36", "?"), helpStyle("1", label), req, def)
}

func printMicrosoftClientIDHelp() {
	if !stdinIsTerminal() {
		return
	}
	fmt.Println()
	fmt.Println(helpSection("Microsoft client ID TLDR:"))
	fmt.Println("  " + helpStyle("36", "1.") + " Entra admin center -> App registrations -> New registration")
	fmt.Println("  " + helpStyle("36", "2.") + " Supported account types: choose org/personal as needed")
	fmt.Println("  " + helpStyle("36", "3.") + " Tenant prompt wants tenant ID or verified domain; common needs multi-tenant app")
	fmt.Println("  " + helpStyle("36", "4.") + " Public client/native app; enable device-code/public client flows")
	fmt.Println("  " + helpStyle("36", "5.") + " API permissions -> Office 365 Exchange Online -> Delegated:")
	fmt.Println("     " + helpStyle("33", oauth.ScopeMicrosoftIMAP))
	fmt.Println("     " + helpStyle("33", oauth.ScopeMicrosoftSMTP))
	fmt.Println("  " + helpStyle("36", "6.") + " Copy Application (client) ID")
	fmt.Println()
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

func microsoftDeviceLogin(tenant, clientID string, scopes []string) (oauth.Token, error) {
	cfg := oauth.MicrosoftConfig{Tenant: tenant, ClientID: clientID, Scopes: scopes}
	code, err := oauth.MicrosoftDeviceCode(context.Background(), cfg)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("%w; hint: use --oauth-tenant TENANT_ID_OR_DOMAIN for single-tenant app registrations", err)
	}
	if strings.TrimSpace(code.Message) != "" {
		fmt.Fprintln(os.Stderr, code.Message)
	} else {
		fmt.Fprintf(os.Stderr, "open %s and enter code %s\n", code.VerificationURI, code.UserCode)
	}
	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timeout := time.Duration(code.ExpiresIn) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	fmt.Fprintln(os.Stderr, "waiting for Microsoft sign-in...")
	token, err := oauth.MicrosoftPollDeviceCode(ctx, cfg, code.DeviceCode, interval)
	if err != nil {
		return oauth.Token{}, err
	}
	if token.RefreshToken == "" {
		return oauth.Token{}, fmt.Errorf("token response missing refresh_token; include %s scope", oauth.ScopeOfflineAccess)
	}
	return token, nil
}

func oauthTenantDefault(email string) string {
	if domain := emailDomain(email); domain != "" {
		return domain
	}
	return "organizations"
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
