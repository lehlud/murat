package protocol

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	"lehnert.dev/murat/internal/oauth"
	"lehnert.dev/murat/internal/store"
)

type IMAPClient struct {
	conn *tls.Conn
	r    *bufio.Reader
	tag  uint64
}

func SyncIMAPS(account store.Account, s *store.Store, limit int, progress func(string)) (int, error) {
	return SyncIMAPSWithUpdater(account, s, limit, progress, nil)
}

func SyncIMAPSWithUpdater(account store.Account, s *store.Store, limit int, progress func(string), updateAccount func(store.Account) error) (int, error) {
	accessToken := ""
	if accountUsesXOAUTH2(account) {
		var changed bool
		var err error
		accessToken, account, changed, err = refreshAccountAccessToken(account, account.OAuthScopes)
		if err != nil {
			return 0, err
		}
		if changed && updateAccount != nil {
			if err := updateAccount(account); err != nil {
				return 0, err
			}
		}
	}
	client, err := dialIMAP(account)
	if err != nil {
		return 0, err
	}
	defer client.close()
	if accessToken != "" {
		if err := client.authenticateXOAUTH2(imapUsername(account), accessToken); err != nil {
			return 0, err
		}
	} else {
		if err := client.login(imapUsername(account), account.Secret); err != nil {
			return 0, err
		}
	}
	folders := imapFolderSet{primary: firstNonEmptyString(account.IMAPMailbox, "INBOX")}
	mailboxes := imapSyncMailboxes(folders.primary)
	if listed, err := client.listMailboxes(); err == nil {
		folders = imapFolders(account.IMAPMailbox, listed)
		mailboxes = imapSyncMailboxes(folders.primary, folders.sent, folders.spam, folders.trash)
	}
	if err := syncIMAPPending(client, account.ID, s, folders); err != nil {
		return 0, err
	}
	known := s.KnownRemoteIDs(account.ID)
	count := 0
	for _, mailbox := range mailboxes {
		added, err := syncIMAPMailbox(client, account.ID, s, mailbox, imapMailboxRole(folders, mailbox), known, limit, progress)
		count += added
		if err != nil {
			return count, err
		}
	}
	return count, s.Flush()
}

func refreshAccountAccessToken(account store.Account, scopes []string) (string, store.Account, bool, error) {
	provider := strings.ToLower(strings.TrimSpace(account.OAuthProvider))
	if provider == "" {
		provider = "microsoft"
	}
	if provider != "microsoft" {
		return "", account, false, fmt.Errorf("unsupported oauth provider: %s", provider)
	}
	token, err := oauth.MicrosoftRefresh(context.Background(), oauth.MicrosoftConfig{
		Tenant:   account.OAuthTenant,
		ClientID: account.OAuthClientID,
		Scopes:   scopes,
	}, account.Secret)
	if err != nil {
		return "", account, false, err
	}
	changed := false
	if token.RefreshToken != "" && token.RefreshToken != account.Secret {
		account.Secret = token.RefreshToken
		changed = true
	}
	if len(scopes) > 0 && !equalScopes(account.OAuthScopes, scopes) {
		account.OAuthScopes = scopes
		changed = true
	}
	return token.AccessToken, account, changed, nil
}

func accountUsesXOAUTH2(account store.Account) bool {
	switch strings.ToLower(strings.TrimSpace(account.AuthKind)) {
	case "xoauth2", "oauth2", "microsoft", "microsoft-oauth2":
		return true
	default:
		return false
	}
}

func equalScopes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func imapUsername(account store.Account) string {
	if username := strings.TrimSpace(account.Username); username != "" {
		return username
	}
	return strings.TrimSpace(account.Email)
}

func syncIMAPMailbox(client *IMAPClient, accountID string, s *store.Store, mailbox, role string, known map[string]bool, limit int, progress func(string)) (int, error) {
	if err := client.selectMailbox(mailbox); err != nil {
		return 0, err
	}
	if progress != nil {
		progress("checking " + mailbox)
	}
	uids, err := client.uidSearchAll()
	if err != nil {
		return 0, err
	}
	if err := syncIMAPKnownState(client, accountID, s, mailbox, role, uids); err != nil {
		return 0, err
	}
	uids = filterUnknownIMAPUIDs(uids, mailbox, known, limit)
	if progress != nil {
		progress(fmt.Sprintf("%d new messages", len(uids)))
	}
	count := 0
	for i, uid := range uids {
		remoteID := imapRemoteID(mailbox, uid)
		if progress != nil {
			progress(fmt.Sprintf("fetch %d/%d uid %s", i+1, len(uids), uid))
		}
		raw, flags, err := client.uidFetch(uid)
		if err != nil {
			return count, err
		}
		msg, err := s.ImportRaw(raw)
		if err != nil {
			return count, err
		}
		msg.SetRemote(accountID, remoteID)
		msg.SetReadSynced(imapFlagsSeen(flags))
		msg.SetFolderSynced([]string{mailbox}, role == "spam", role == "trash")
		known[remoteID] = true
		count++
	}
	return count, nil
}

func imapSyncMailboxes(primary string, extras ...string) []string {
	if strings.TrimSpace(primary) == "" {
		primary = "INBOX"
	}
	seen := map[string]bool{}
	out := []string{}
	for _, mailbox := range append([]string{primary}, extras...) {
		mailbox = strings.TrimSpace(mailbox)
		if mailbox == "" {
			continue
		}
		key := strings.ToLower(mailbox)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, mailbox)
	}
	return out
}

type imapFolderSet struct {
	primary string
	sent    string
	spam    string
	trash   string
}

type imapMailboxInfo struct {
	name  string
	inbox bool
	sent  bool
	spam  bool
	trash bool
}

func imapFolders(primary string, boxes []imapMailboxInfo) imapFolderSet {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		primary = "INBOX"
	}
	folders := imapFolderSet{primary: primary}
	for _, box := range boxes {
		if folders.primary == "" && box.inbox {
			folders.primary = box.name
		}
		if folders.sent == "" && box.sent {
			folders.sent = box.name
		}
		if folders.spam == "" && box.spam {
			folders.spam = box.name
		}
		if folders.trash == "" && box.trash {
			folders.trash = box.name
		}
	}
	return folders
}

func imapMailboxRole(folders imapFolderSet, mailbox string) string {
	switch strings.ToLower(strings.TrimSpace(mailbox)) {
	case strings.ToLower(strings.TrimSpace(folders.sent)):
		return "sent"
	case strings.ToLower(strings.TrimSpace(folders.spam)):
		return "spam"
	case strings.ToLower(strings.TrimSpace(folders.trash)):
		return "trash"
	default:
		return "inbox"
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func syncIMAPKnownState(client *IMAPClient, accountID string, s *store.Store, mailbox, role string, uids []string) error {
	knownUIDs := []string{}
	for _, uid := range uids {
		if _, ok := s.RemoteMessage(accountID, imapRemoteID(mailbox, uid)); ok {
			knownUIDs = append(knownUIDs, uid)
		}
	}
	flagsByUID, err := client.uidFetchFlags(knownUIDs)
	if err != nil {
		return err
	}
	for uid, flags := range flagsByUID {
		msg, ok := s.RemoteMessage(accountID, imapRemoteID(mailbox, uid))
		if !ok {
			continue
		}
		seen := imapFlagsSeen(flags)
		if msg.ReadDirty {
			if err := client.uidStoreSeen(uid, msg.Read); err != nil {
				return err
			}
			msg.ClearReadDirty()
		} else {
			msg.SetReadSynced(seen)
		}
		if !msg.FolderDirty {
			msg.SetFolderSynced([]string{mailbox}, role == "spam", role == "trash")
		}
	}
	return nil
}

func syncIMAPPending(client *IMAPClient, accountID string, s *store.Store, folders imapFolderSet) error {
	selected := ""
	selectMailbox := func(mailbox string) error {
		if mailbox == "" || mailbox == selected {
			return nil
		}
		if err := client.selectMailbox(mailbox); err != nil {
			return err
		}
		selected = mailbox
		return nil
	}
	for _, msg := range s.MessagesForAccount(accountID) {
		if msg.RemoteID == "" && msg.IsSent() && msg.FolderDirty && folders.sent != "" {
			raw, err := s.RawMessage(msg)
			if err != nil {
				return err
			}
			if err := client.appendMessage(folders.sent, raw, true); err != nil {
				return err
			}
			msg.ClearReadDirty()
			msg.ClearFolderDirty()
			continue
		}
		mailbox, uid, ok := parseIMAPRemoteID(msg.RemoteID)
		if !ok {
			continue
		}
		if msg.ReadDirty {
			if err := selectMailbox(mailbox); err != nil {
				return err
			}
			if err := client.uidStoreSeen(uid, msg.Read); err != nil {
				return err
			}
			msg.ClearReadDirty()
		}
		if !msg.FolderDirty {
			continue
		}
		dest := desiredIMAPMailbox(msg, folders)
		if dest == "" {
			continue
		}
		if strings.EqualFold(dest, mailbox) {
			msg.ClearFolderDirty()
			continue
		}
		if err := selectMailbox(mailbox); err != nil {
			return err
		}
		if err := client.uidCopy(uid, dest); err != nil {
			return err
		}
		if err := client.uidStoreDeleted(uid); err != nil {
			return err
		}
		if err := client.expunge(); err != nil {
			return err
		}
		msg.ClearFolderDirty()
	}
	return nil
}

func desiredIMAPMailbox(msg *store.Message, folders imapFolderSet) string {
	if msg.Trashed {
		return folders.trash
	}
	if msg.IsSpam() {
		return folders.spam
	}
	if msg.IsSent() {
		return folders.sent
	}
	return folders.primary
}

func parseIMAPRemoteID(remoteID string) (string, string, bool) {
	if !strings.HasPrefix(remoteID, "imap:") {
		return "", "", false
	}
	value := strings.TrimPrefix(remoteID, "imap:")
	i := strings.LastIndex(value, ":")
	if i <= 0 || i == len(value)-1 {
		return "", "", false
	}
	return value[:i], value[i+1:], true
}

func filterUnknownIMAPUIDs(uids []string, mailbox string, known map[string]bool, limit int) []string {
	out := make([]string, 0, len(uids))
	maxKnown, hasKnown := maxKnownIMAPUID(mailbox, known)
	for _, uid := range uids {
		uidNumber, err := strconv.ParseUint(uid, 10, 64)
		if hasKnown && (err != nil || uidNumber <= maxKnown) {
			continue
		}
		if !known[imapRemoteID(mailbox, uid)] {
			out = append(out, uid)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func imapRemoteID(mailbox, uid string) string {
	return "imap:" + mailbox + ":" + uid
}

func maxKnownIMAPUID(mailbox string, known map[string]bool) (uint64, bool) {
	prefix := "imap:" + mailbox + ":"
	var max uint64
	found := false
	for remoteID := range known {
		uid, err := strconv.ParseUint(strings.TrimPrefix(remoteID, prefix), 10, 64)
		if err != nil || !strings.HasPrefix(remoteID, prefix) {
			continue
		}
		if !found || uid > max {
			max = uid
			found = true
		}
	}
	return max, found
}

func dialIMAP(account store.Account) (*IMAPClient, error) {
	port := account.IMAPPort
	if port == 0 {
		port = 993
	}
	addr := fmt.Sprintf("%s:%d", account.IMAPHost, port)
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: account.IMAPHost, MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, err
	}
	client := &IMAPClient{conn: conn, r: bufio.NewReader(conn)}
	if _, err := client.r.ReadString('\n'); err != nil {
		conn.Close()
		return nil, err
	}
	return client, nil
}

func (c *IMAPClient) close() { _ = c.conn.Close() }

func (c *IMAPClient) nextTag() string {
	n := atomic.AddUint64(&c.tag, 1)
	return fmt.Sprintf("A%04d", n)
}

func (c *IMAPClient) command(format string, args ...any) ([]string, error) {
	tag := c.nextTag()
	cmd := fmt.Sprintf(format, args...)
	if _, err := fmt.Fprintf(c.conn, "%s %s\r\n", tag, cmd); err != nil {
		return nil, err
	}
	lines := []string{}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return lines, err
		}
		line = strings.TrimRight(line, "\r\n")
		lines = append(lines, line)
		if strings.HasPrefix(line, tag+" ") {
			if strings.Contains(line, " OK") {
				return lines, nil
			}
			return lines, fmt.Errorf("imap command failed: %s", line)
		}
	}
}

func (c *IMAPClient) login(username, password string) error {
	_, err := c.command("LOGIN %s %s", quoteIMAP(username), quoteIMAP(password))
	return err
}

func (c *IMAPClient) authenticateXOAUTH2(username, accessToken string) error {
	tag := c.nextTag()
	if _, err := fmt.Fprintf(c.conn, "%s AUTHENTICATE XOAUTH2 %s\r\n", tag, xoauth2InitialResponse(username, accessToken)); err != nil {
		return err
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "+") {
			if _, err := fmt.Fprintf(c.conn, "\r\n"); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.Contains(line, " OK") {
				return nil
			}
			return fmt.Errorf("imap xoauth2 failed: %s", line)
		}
	}
}

func xoauth2InitialResponse(username, accessToken string) string {
	return base64.StdEncoding.EncodeToString(xoauth2SASL(username, accessToken))
}

func xoauth2SASL(username, accessToken string) []byte {
	return []byte("user=" + username + "\x01auth=Bearer " + accessToken + "\x01\x01")
}

func (c *IMAPClient) selectMailbox(mailbox string) error {
	_, err := c.command("SELECT %s", quoteIMAP(mailbox))
	return err
}

func (c *IMAPClient) listMailboxes() ([]imapMailboxInfo, error) {
	lines, err := c.command("LIST \"\" \"*\"")
	if err != nil {
		return nil, err
	}
	out := []imapMailboxInfo{}
	for _, line := range lines {
		mailbox, ok := parseIMAPListMailbox(line)
		if ok {
			out = append(out, mailbox)
		}
	}
	return out, nil
}

func parseIMAPListMailbox(line string) (imapMailboxInfo, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(strings.ToUpper(line), "* LIST ") {
		return imapMailboxInfo{}, false
	}
	flagsStart := strings.Index(line, "(")
	if flagsStart < 0 {
		return imapMailboxInfo{}, false
	}
	flagsEndRel := strings.Index(line[flagsStart:], ")")
	if flagsEndRel < 0 {
		return imapMailboxInfo{}, false
	}
	flagsEnd := flagsStart + flagsEndRel
	flags := strings.ToLower(line[flagsStart+1 : flagsEnd])
	rest := strings.TrimSpace(line[flagsEnd+1:])
	_, rest, ok := readIMAPListToken(rest)
	if !ok {
		return imapMailboxInfo{}, false
	}
	name, _, ok := readIMAPListToken(rest)
	if !ok || strings.TrimSpace(name) == "" {
		return imapMailboxInfo{}, false
	}
	return imapMailboxInfo{
		name:  name,
		inbox: strings.Contains(flags, `\inbox`) || strings.EqualFold(imapMailboxBase(name), "inbox"),
		sent:  strings.Contains(flags, `\sent`) || sentIMAPMailboxName(name),
		spam:  strings.Contains(flags, `\junk`) || strings.Contains(flags, `\spam`) || spamIMAPMailboxName(name),
		trash: strings.Contains(flags, `\trash`) || trashIMAPMailboxName(name),
	}, true
}

func readIMAPListToken(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", false
	}
	if text[0] != '"' {
		fields := strings.Fields(text)
		if len(fields) == 0 {
			return "", "", false
		}
		return fields[0], strings.TrimSpace(strings.TrimPrefix(text, fields[0])), true
	}
	var out strings.Builder
	escaped := false
	for i := 1; i < len(text); i++ {
		b := text[i]
		if escaped {
			out.WriteByte(b)
			escaped = false
			continue
		}
		if b == '\\' {
			escaped = true
			continue
		}
		if b == '"' {
			return out.String(), strings.TrimSpace(text[i+1:]), true
		}
		out.WriteByte(b)
	}
	return "", "", false
}

func sentIMAPMailbox(boxes []imapMailboxInfo) string {
	for _, box := range boxes {
		if box.sent {
			return box.name
		}
	}
	return ""
}

func spamIMAPMailboxName(name string) bool {
	base := strings.ToLower(imapMailboxBase(name))
	switch base {
	case "spam", "junk", "junk e-mail", "junk mail", "bulk mail":
		return true
	default:
		return false
	}
}

func trashIMAPMailboxName(name string) bool {
	base := strings.ToLower(imapMailboxBase(name))
	switch base {
	case "trash", "bin", "deleted", "deleted items", "gelöscht", "geloescht", "gelöschte elemente", "geloeschte elemente", "gelöschte objekte", "geloeschte objekte", "papierkorb":
		return true
	default:
		return false
	}
}

func sentIMAPMailboxName(name string) bool {
	base := strings.ToLower(imapMailboxBase(name))
	switch base {
	case "sent", "sent mail", "sent messages", "sent items", "sent-mail", "sentmail", "outbox", "gesendet", "gesendete elemente", "gesendete objekte":
		return true
	default:
		return false
	}
}

func imapMailboxBase(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	parts := strings.Split(name, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if part := strings.TrimSpace(parts[i]); part != "" {
			return part
		}
	}
	return name
}

func (c *IMAPClient) uidSearchAll() ([]string, error) {
	lines, err := c.command("UID SEARCH ALL")
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "* SEARCH") {
			fields := strings.Fields(line)
			if len(fields) <= 2 {
				return nil, nil
			}
			return fields[2:], nil
		}
	}
	return nil, nil
}

func (c *IMAPClient) uidFetch(uid string) ([]byte, string, error) {
	tag := c.nextTag()
	if _, err := fmt.Fprintf(c.conn, "%s UID FETCH %s (FLAGS RFC822)\r\n", tag, uid); err != nil {
		return nil, "", err
	}
	var raw []byte
	flags := ""
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, flags, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, tag+" ") {
			if strings.Contains(trimmed, " OK") {
				return raw, flags, nil
			}
			return nil, flags, fmt.Errorf("imap fetch failed: %s", trimmed)
		}
		if strings.Contains(strings.ToUpper(trimmed), "FLAGS") {
			flags = trimmed
		}
		literal := literalSize(trimmed)
		if literal <= 0 {
			continue
		}
		raw = make([]byte, literal)
		if _, err := io.ReadFull(c.r, raw); err != nil {
			return nil, flags, err
		}
		// Consume trailing CRLF after literal if present.
		_, _ = c.r.ReadString('\n')
	}
}

func (c *IMAPClient) uidFetchFlags(uids []string) (map[string]string, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for start := 0; start < len(uids); start += 200 {
		end := min(start+200, len(uids))
		lines, err := c.command("UID FETCH %s (FLAGS)", strings.Join(uids[start:end], ","))
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			uid, flags, ok := parseIMAPFetchFlags(line)
			if ok {
				out[uid] = flags
			}
		}
	}
	return out, nil
}

func parseIMAPFetchFlags(line string) (string, string, bool) {
	upper := strings.ToUpper(line)
	uidIndex := strings.Index(upper, "UID ")
	flagsIndex := strings.Index(upper, "FLAGS ")
	if !strings.HasPrefix(strings.TrimSpace(upper), "* ") || uidIndex < 0 || flagsIndex < 0 {
		return "", "", false
	}
	uidRest := strings.Fields(line[uidIndex+4:])
	if len(uidRest) == 0 {
		return "", "", false
	}
	flagsStart := strings.Index(line[flagsIndex:], "(")
	flagsEnd := strings.Index(line[flagsIndex:], ")")
	if flagsStart < 0 || flagsEnd < flagsStart {
		return uidRest[0], "", true
	}
	flags := line[flagsIndex+flagsStart : flagsIndex+flagsEnd+1]
	return strings.Trim(uidRest[0], ")"), flags, true
}

func imapFlagsSeen(flags string) bool {
	return strings.Contains(strings.ToLower(flags), `\seen`)
}

func (c *IMAPClient) uidStoreSeen(uid string, read bool) error {
	op := "+FLAGS.SILENT"
	if !read {
		op = "-FLAGS.SILENT"
	}
	_, err := c.command("UID STORE %s %s (\\Seen)", uid, op)
	return err
}

func (c *IMAPClient) uidStoreDeleted(uid string) error {
	_, err := c.command("UID STORE %s +FLAGS.SILENT (\\Deleted)", uid)
	return err
}

func (c *IMAPClient) uidCopy(uid, mailbox string) error {
	_, err := c.command("UID COPY %s %s", uid, quoteIMAP(mailbox))
	return err
}

func (c *IMAPClient) expunge() error {
	_, err := c.command("EXPUNGE")
	return err
}

func (c *IMAPClient) appendMessage(mailbox string, raw []byte, read bool) error {
	raw = []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n", "\r\n"))
	flags := ""
	if read {
		flags = " (\\Seen)"
	}
	tag := c.nextTag()
	if _, err := fmt.Fprintf(c.conn, "%s APPEND %s%s {%d}\r\n", tag, quoteIMAP(mailbox), flags, len(raw)); err != nil {
		return err
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "+") {
		return fmt.Errorf("imap append rejected: %s", strings.TrimSpace(line))
	}
	if _, err := c.conn.Write(raw); err != nil {
		return err
	}
	if _, err := c.conn.Write([]byte("\r\n")); err != nil {
		return err
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, tag+" ") {
			if strings.Contains(line, " OK") {
				return nil
			}
			return fmt.Errorf("imap append failed: %s", line)
		}
	}
}

func literalSize(line string) int {
	start := strings.LastIndex(line, "{")
	end := strings.LastIndex(line, "}")
	if start < 0 || end < start {
		return 0
	}
	n, _ := strconv.Atoi(line[start+1 : end])
	return n
}

func quoteIMAP(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
