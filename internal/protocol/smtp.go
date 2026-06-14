package protocol

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

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
	Attachments []Attachment
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

func SendSMTPS(account store.Account, draft Draft) error {
	host := account.SMTPHost
	port := account.SMTPPort
	if port == 0 {
		port = 465
	}
	if host == "" {
		return fmt.Errorf("smtp_host missing")
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
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
	if username != "" || secret != "" {
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
	_, err = w.Write([]byte(message(account, draft)))
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	return err
}

func message(account store.Account, draft Draft) string {
	if len(draft.Attachments) > 0 {
		return multipartMessage(account, draft)
	}
	return strings.Join([]string{
		"From: " + draftFrom(account, draft),
		"To: " + draft.To,
		"Cc: " + draft.Cc,
		"Subject: " + draft.Subject,
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		draft.Body,
	}, "\r\n")
}

func multipartMessage(account store.Account, draft Draft) string {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	headers := messageHeaders(account, draft)
	headers = append(headers, "Content-Type: multipart/mixed; boundary="+writer.Boundary())
	body.WriteString(strings.Join(headers, "\r\n"))
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
		contentType := attachment.ContentType
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
		"From: " + draftFrom(account, draft),
		"To: " + draft.To,
		"Cc: " + draft.Cc,
		"Subject: " + draft.Subject,
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
	from := draftFrom(account, draft)
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
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "\"", "\\\"")
}

func recipients(draft Draft) []string {
	values := []string{draft.To, draft.Cc, draft.Bcc}
	out := []string{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}
