package protocol

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"lehnert.dev/murat/internal/oauth"
	"lehnert.dev/murat/internal/store"
)

type Draft struct {
	From        string
	To          string
	Cc          string
	Bcc         string
	Subject     string
	Body        string
	PGP         string
	RawMIME     string
	Attachments []Attachment
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

func SendSMTPS(account store.Account, draft Draft) error {
	return SendSMTPSWithUpdater(account, draft, nil)
}

func SendSMTPSWithUpdater(account store.Account, draft Draft, updateAccount func(store.Account) error) error {
	host := account.SMTPHost
	port := account.SMTPPort
	if port == 0 {
		port = 465
	}
	if host == "" {
		return fmt.Errorf("smtp_host missing")
	}
	accessToken := ""
	if accountUsesXOAUTH2(account) {
		var changed bool
		var err error
		accessToken, account, changed, err = refreshAccountAccessToken(account, smtpOAuthScopes(account.OAuthScopes))
		if err != nil {
			return err
		}
		if changed && updateAccount != nil {
			if err := updateAccount(account); err != nil {
				return err
			}
		}
	}
	client, err := dialSMTP(host, port)
	if err != nil {
		return err
	}
	defer client.Quit()
	username := account.SMTPUsername
	if username == "" {
		username = account.Username
	}
	secret := account.SMTPSecret
	if secret == "" {
		secret = account.Secret
	}
	if accessToken != "" {
		if err := client.Auth(xoauth2SMTPAuth{username: smtpUsername(account), accessToken: accessToken}); err != nil {
			return err
		}
	} else if username != "" || secret != "" {
		if err := client.Auth(smtp.PlainAuth("", username, secret, host)); err != nil {
			return err
		}
	}
	from := draftFromEmail(account, draft)
	if err := client.Mail(from); err != nil {
		return err
	}
	recipients := recipients(draft)
	if len(recipients) == 0 {
		return fmt.Errorf("add at least one recipient")
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(Message(account, draft)))
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	return err
}

func dialSMTP(host string, port int) (*smtp.Client, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	if port == 587 {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = client.Close()
			return nil, err
		}
		return client, nil
	}
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

type xoauth2SMTPAuth struct {
	username    string
	accessToken string
}

func (a xoauth2SMTPAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "XOAUTH2", xoauth2SASL(a.username, a.accessToken), nil
}

func (a xoauth2SMTPAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("smtp xoauth2 failed: %s", strings.TrimSpace(string(fromServer)))
	}
	return nil, nil
}

func smtpUsername(account store.Account) string {
	if username := strings.TrimSpace(account.SMTPUsername); username != "" {
		return username
	}
	return imapUsername(account)
}

func smtpOAuthScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return oauth.DefaultMicrosoftMailScopes()
	}
	out := append([]string(nil), scopes...)
	if !hasScope(out, oauth.ScopeMicrosoftSMTP) {
		out = append(out, oauth.ScopeMicrosoftSMTP)
	}
	if !hasScope(out, oauth.ScopeOfflineAccess) {
		out = append(out, oauth.ScopeOfflineAccess)
	}
	return out
}

func hasScope(scopes []string, scope string) bool {
	for _, item := range scopes {
		if item == scope {
			return true
		}
	}
	return false
}

func Message(account store.Account, draft Draft) string {
	if strings.TrimSpace(draft.RawMIME) != "" {
		return messageWithMIME(account, draft, draft.RawMIME)
	}
	if len(draft.Attachments) > 0 {
		return messageWithMIME(account, draft, MIMEEntity(draft))
	}
	return messageWithMIME(account, draft, MIMEEntity(draft))
}

func messageWithMIME(account store.Account, draft Draft, entity string) string {
	entity = normalizeMIMEEntity(entity)
	entityHeaders, entityBody := splitMIMEEntity(entity)
	headers := append([]string(nil), messageHeaders(account, draft)...)
	headers = append(headers, entityHeaders...)
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + strings.ReplaceAll(entityBody, "\n", "\r\n")
}

func normalizeMIMEEntity(entity string) string {
	entity = strings.ReplaceAll(entity, "\r\n", "\n")
	entity = strings.ReplaceAll(entity, "\r", "\n")
	return strings.TrimLeft(entity, "\n")
}

func splitMIMEEntity(entity string) ([]string, string) {
	head, body, ok := strings.Cut(entity, "\n\n")
	if !ok {
		return nil, entity
	}
	headers := []string{}
	for _, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) != "" {
			headers = append(headers, line)
		}
	}
	return headers, body
}

func MIMEEntity(draft Draft) string {
	if len(draft.Attachments) > 0 {
		return multipartEntity(draft)
	}
	return strings.Join([]string{
		"Content-Type: text/plain; charset=utf-8",
		"",
		draft.Body,
	}, "\r\n")
}

func multipartEntity(draft Draft) string {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	body.WriteString("Content-Type: multipart/mixed; boundary=" + writer.Boundary())
	body.WriteString("\r\n\r\n")

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	textPart, _ := writer.CreatePart(textHeader)
	_, _ = textPart.Write([]byte(draft.Body))

	for _, attachment := range draft.Attachments {
		name := attachment.Filename
		if strings.TrimSpace(name) == "" {
			name = "attachment"
		}
		contentType := cleanHeaderValue(attachment.ContentType)
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/octet-stream"
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", contentType+"; name=\""+escapeMIMEParam(name)+"\"")
		header.Set("Content-Disposition", "attachment; filename=\""+escapeMIMEParam(name)+"\"")
		header.Set("Content-Transfer-Encoding", "base64")
		part, _ := writer.CreatePart(header)
		writeBase64(part, attachment.Data)
	}
	_ = writer.Close()
	return body.String()
}

func messageHeaders(account store.Account, draft Draft) []string {
	return []string{
		"From: " + cleanHeaderValue(draftFrom(account, draft)),
		"To: " + cleanHeaderValue(draft.To),
		"Cc: " + cleanHeaderValue(draft.Cc),
		"Subject: " + cleanHeaderValue(draft.Subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
	}
}

func draftFrom(account store.Account, draft Draft) string {
	if strings.TrimSpace(draft.From) != "" {
		return strings.TrimSpace(draft.From)
	}
	return account.Email
}

func draftFromEmail(account store.Account, draft Draft) string {
	from := cleanHeaderValue(draftFrom(account, draft))
	if addr, err := mail.ParseAddress(from); err == nil {
		return addr.Address
	}
	return from
}

func writeBase64(writer interface{ Write([]byte) (int, error) }, data []byte) {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	base64.StdEncoding.Encode(encoded, data)
	for len(encoded) > 76 {
		_, _ = writer.Write(encoded[:76])
		_, _ = writer.Write([]byte("\r\n"))
		encoded = encoded[76:]
	}
	_, _ = writer.Write(encoded)
	_, _ = writer.Write([]byte("\r\n"))
}

func escapeMIMEParam(value string) string {
	value = cleanHeaderValue(value)
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "\"", "\\\"")
}

func recipients(draft Draft) []string {
	values := []string{draft.To, draft.Cc, draft.Bcc}
	out := []string{}
	for _, value := range values {
		value = cleanHeaderValue(value)
		if strings.TrimSpace(value) == "" {
			continue
		}
		if addresses, err := mail.ParseAddressList(value); err == nil {
			for _, address := range addresses {
				if email := cleanHeaderValue(strings.TrimSpace(address.Address)); email != "" {
					out = append(out, email)
				}
			}
			continue
		}
		for _, item := range strings.Split(value, ",") {
			item = cleanHeaderValue(strings.TrimSpace(item))
			if address, err := mail.ParseAddress(item); err == nil {
				item = cleanHeaderValue(strings.TrimSpace(address.Address))
			}
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}
