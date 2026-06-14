package protocol

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	"lehnert.dev/murat/internal/store"
)

type IMAPClient struct {
	conn *tls.Conn
	r    *bufio.Reader
	tag  uint64
}

func SyncIMAPS(account store.Account, s *store.Store, limit int, progress func(string)) (int, error) {
	client, err := dialIMAP(account)
	if err != nil {
		return 0, err
	}
	defer client.close()
	if err := client.login(account.Username, account.Secret); err != nil {
		return 0, err
	}
	mailbox := account.IMAPMailbox
	if mailbox == "" {
		mailbox = "INBOX"
	}
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
	known := s.KnownRemoteIDs(account.ID)
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
		msg.SetRemote(account.ID, remoteID)
		msg.SetRead(strings.Contains(strings.ToLower(flags), "\\seen"))
		msg.SetTags([]string{mailbox})
		known[remoteID] = true
		count++
	}
	return count, s.Flush()
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

func (c *IMAPClient) selectMailbox(mailbox string) error {
	_, err := c.command("SELECT %s", quoteIMAP(mailbox))
	return err
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
