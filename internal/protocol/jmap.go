package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"lehnert.dev/murat/internal/store"
)

const capMail = "urn:ietf:params:jmap:mail"
const capSubmission = "urn:ietf:params:jmap:submission"

type jmapSession struct {
	APIURL          string            `json:"apiUrl"`
	UploadURL       string            `json:"uploadUrl"`
	DownloadURL     string            `json:"downloadUrl"`
	PrimaryAccounts map[string]string `json:"primaryAccounts"`
}

type jmapAttachmentData struct {
	Filename    string
	ContentType string
	BlobID      string
	Data        []byte
}

type jmapUploadResponse struct {
	BlobID string `json:"blobId"`
	Type   string `json:"type"`
	Size   int    `json:"size"`
}

type jmapRequest struct {
	Using       []string `json:"using"`
	MethodCalls [][]any  `json:"methodCalls"`
}

type jmapResponse struct {
	MethodResponses [][]json.RawMessage `json:"methodResponses"`
}

func SyncJMAP(account store.Account, s *store.Store, limit int, progress func(string)) (int, error) {
	if progress != nil {
		progress("checking session")
	}
	session, err := fetchSession(account)
	if err != nil {
		return 0, err
	}
	mailAccount := session.PrimaryAccounts[capMail]
	if mailAccount == "" {
		return 0, fmt.Errorf("no JMAP mail account")
	}
	if progress != nil {
		progress("checking mailboxes")
	}
	mailboxes, _ := jmapMailboxList(account, session.APIURL, mailAccount)
	if len(mailboxes) > 0 {
		s.SetMailboxes(account.ID, mailboxes)
	}
	roles := jmapMailboxRoles(mailboxes)
	if err := syncJMAPLocal(account, session.APIURL, mailAccount, roles, s); err != nil {
		return 0, err
	}
	if progress != nil {
		progress("checking messages")
	}
	ids, err := jmapQuery(account, session.APIURL, mailAccount, limit)
	if err != nil {
		return 0, err
	}
	known := s.KnownRemoteIDs(account.ID)
	if err := syncJMAPRemoteState(account, session.APIURL, mailAccount, account.ID, s, ids, roles); err != nil {
		return 0, err
	}
	ids = jmapFetchIDs(ids, account.ID, s, known, limit)
	if progress != nil {
		progress(fmt.Sprintf("%d messages to fetch", len(ids)))
	}
	if len(ids) == 0 {
		return 0, s.Flush()
	}
	if progress != nil {
		progress(fmt.Sprintf("fetching %d messages", len(ids)))
	}
	items, err := jmapGet(account, session.APIURL, mailAccount, ids)
	if err != nil {
		return 0, err
	}
	count := 0
	for i, item := range items {
		if progress != nil {
			progress(fmt.Sprintf("import %d/%d", i+1, len(items)))
		}
		remoteID := jmapRemoteID(stringValue(item["id"]))
		existing, replaceExisting := s.RemoteMessage(account.ID, remoteID)
		if known[remoteID] && (!replaceExisting || existing.HasAttachment || !jmapHasAttachment(item)) {
			continue
		}
		raw, err := jmapEmailToEML(account, session, mailAccount, item)
		if err != nil {
			return 0, err
		}
		msg, err := s.ImportRaw([]byte(raw))
		if err != nil {
			return 0, err
		}
		msg.SetRemote(account.ID, remoteID)
		read := jmapRead(item)
		tags := jmapTags(item)
		spam, trashed := jmapFolderState(tags, roles)
		if replaceExisting {
			if !existing.ReadDirty {
				msg.SetReadSynced(read)
			}
			if !existing.FolderDirty {
				msg.SetFolderSynced(tags, spam, trashed)
			}
			if existing.Key != msg.Key {
				_ = s.RemoveMessage(existing.Key, true)
			}
		} else {
			msg.SetReadSynced(read)
			msg.SetFolderSynced(tags, spam, trashed)
		}
		if remoteID != "" {
			known[remoteID] = true
		}
		count++
	}
	return count, s.Flush()
}

func jmapFetchIDs(ids []string, accountID string, s *store.Store, known map[string]bool, limit int) []string {
	out := filterUnknownJMAPIDs(ids, known, limit)
	seen := map[string]bool{}
	for _, id := range out {
		seen[id] = true
	}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		remoteID := jmapRemoteID(id)
		if !known[remoteID] {
			continue
		}
		if msg, ok := s.RemoteMessage(accountID, remoteID); ok && !msg.HasAttachment {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

type jmapRoleIDs struct {
	inbox string
	sent  string
	spam  string
	trash string
}

func jmapMailboxRoles(mailboxes []map[string]any) jmapRoleIDs {
	roles := jmapRoleIDs{}
	for _, mailbox := range mailboxes {
		id := stringValue(mailbox["id"])
		if id == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringValue(mailbox["role"])))
		if role == "" {
			role = jmapMailboxNameRole(stringValue(mailbox["name"]))
		}
		switch role {
		case "inbox":
			if roles.inbox == "" {
				roles.inbox = id
			}
		case "sent":
			if roles.sent == "" {
				roles.sent = id
			}
		case "junk", "spam":
			if roles.spam == "" {
				roles.spam = id
			}
		case "trash", "deleted":
			if roles.trash == "" {
				roles.trash = id
			}
		}
	}
	return roles
}

func jmapMailboxNameRole(name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	slash := strings.LastIndexAny(base, `/\\`)
	if slash >= 0 {
		base = strings.TrimSpace(base[slash+1:])
	}
	switch base {
	case "inbox", "posteingang":
		return "inbox"
	case "sent", "sent mail", "sent messages", "sent items", "sent-mail", "sentmail", "outbox", "gesendet", "gesendete elemente", "gesendete objekte":
		return "sent"
	case "spam", "junk", "junk e-mail", "junk mail", "bulk mail":
		return "junk"
	case "trash", "bin", "deleted", "deleted items", "gelöscht", "geloescht", "gelöschte elemente", "geloeschte elemente", "gelöschte objekte", "geloeschte objekte", "papierkorb":
		return "trash"
	default:
		return ""
	}
}

func syncJMAPLocal(account store.Account, apiURL, mailAccount string, roles jmapRoleIDs, s *store.Store) error {
	for _, msg := range s.MessagesForAccount(account.ID) {
		id := jmapIDFromRemoteID(msg.RemoteID)
		if id == "" {
			continue
		}
		updates := map[string]any{}
		if msg.ReadDirty {
			if msg.Read {
				updates["keywords/$seen"] = true
			} else {
				updates["keywords/$seen"] = nil
			}
		}
		dest, role := desiredJMAPMailbox(msg, roles)
		if msg.FolderDirty && dest != "" {
			for _, tag := range msg.Tags {
				if strings.TrimSpace(tag) != "" {
					updates[jmapPatchPath("mailboxIds", tag)] = nil
				}
			}
			updates[jmapPatchPath("mailboxIds", dest)] = true
		}
		if len(updates) == 0 {
			continue
		}
		if err := jmapUpdateEmail(account, apiURL, mailAccount, id, updates); err != nil {
			return err
		}
		if msg.ReadDirty {
			msg.ClearReadDirty()
		}
		if msg.FolderDirty && dest != "" {
			msg.SetFolderSynced([]string{dest}, role == "spam", role == "trash")
		}
	}
	return nil
}

func syncJMAPRemoteState(account store.Account, apiURL, mailAccount, accountID string, s *store.Store, ids []string, roles jmapRoleIDs) error {
	state, err := jmapGetState(account, apiURL, mailAccount, ids)
	if err != nil {
		return err
	}
	for _, item := range state {
		remoteID := jmapRemoteID(stringValue(item["id"]))
		msg, ok := s.RemoteMessage(accountID, remoteID)
		if !ok {
			continue
		}
		if !msg.ReadDirty {
			msg.SetReadSynced(jmapRead(item))
		}
		if !msg.FolderDirty {
			tags := jmapTags(item)
			spam, trash := jmapFolderState(tags, roles)
			msg.SetFolderSynced(tags, spam, trash)
		}
	}
	return nil
}

func jmapGetState(account store.Account, apiURL, mailAccount string, ids []string) ([]map[string]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	idsAny := make([]any, 0, len(ids))
	for _, id := range ids {
		idsAny = append(idsAny, id)
	}
	body := jmapRequest{Using: []string{capMail}, MethodCalls: [][]any{{"Email/get", map[string]any{"accountId": mailAccount, "ids": idsAny, "properties": []string{"id", "keywords", "mailboxIds"}}, "g"}}}
	var response jmapResponse
	if err := httpJSON(account, "POST", apiURL, body, &response); err != nil {
		return nil, err
	}
	argsOut, err := methodArgs(response, "g")
	if err != nil {
		return nil, err
	}
	listAny, _ := argsOut["list"].([]any)
	out := make([]map[string]any, 0, len(listAny))
	for _, item := range listAny {
		if value, ok := item.(map[string]any); ok {
			out = append(out, value)
		}
	}
	return out, nil
}

func jmapUpdateEmail(account store.Account, apiURL, mailAccount, id string, updates map[string]any) error {
	body := jmapRequest{Using: []string{capMail}, MethodCalls: [][]any{{"Email/set", map[string]any{"accountId": mailAccount, "update": map[string]any{id: updates}}, "u"}}}
	var response jmapResponse
	if err := httpJSON(account, "POST", apiURL, body, &response); err != nil {
		return err
	}
	_, err := methodArgs(response, "u")
	return err
}

func desiredJMAPMailbox(msg *store.Message, roles jmapRoleIDs) (string, string) {
	if msg.Trashed {
		return roles.trash, "trash"
	}
	if msg.IsSpam() {
		return roles.spam, "spam"
	}
	if msg.IsSent() {
		return roles.sent, "sent"
	}
	return roles.inbox, "inbox"
}

func jmapFolderState(tags []string, roles jmapRoleIDs) (bool, bool) {
	spam := false
	trash := false
	for _, tag := range tags {
		if tag == roles.spam && tag != "" {
			spam = true
		}
		if tag == roles.trash && tag != "" {
			trash = true
		}
	}
	return spam, trash
}

func jmapIDFromRemoteID(remoteID string) string {
	if !strings.HasPrefix(remoteID, "jmap:") {
		return ""
	}
	return strings.TrimPrefix(remoteID, "jmap:")
}

func jmapPatchPath(prefix, key string) string {
	key = strings.ReplaceAll(key, "~", "~0")
	key = strings.ReplaceAll(key, "/", "~1")
	return prefix + "/" + key
}

func filterUnknownJMAPIDs(ids []string, known map[string]bool, limit int) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !known[jmapRemoteID(id)] {
			out = append(out, id)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func jmapRemoteID(id string) string {
	if id == "" {
		return ""
	}
	return "jmap:" + id
}

func SendJMAP(account store.Account, draft Draft) error {
	session, err := fetchSession(account)
	if err != nil {
		return err
	}
	mailAccount := session.PrimaryAccounts[capMail]
	if mailAccount == "" {
		return fmt.Errorf("no JMAP mail account")
	}
	submissionAccount := session.PrimaryAccounts[capSubmission]
	if submissionAccount == "" {
		submissionAccount = mailAccount
	}
	senderEmail := draftFromEmail(account, draft)
	identityID, err := jmapIdentity(account, session.APIURL, submissionAccount, senderEmail)
	if err != nil {
		return err
	}
	draftsID, sentID, err := jmapMailboxes(account, session.APIURL, mailAccount)
	if err != nil {
		return err
	}
	if draftsID == "" {
		return fmt.Errorf("server did not advertise Drafts mailbox")
	}
	cleanup := map[string]any{"keywords/$draft": nil}
	if sentID != "" {
		cleanup["mailboxIds/"+draftsID] = nil
		cleanup["mailboxIds/"+sentID] = true
	}
	if strings.TrimSpace(draft.RawMIME) != "" {
		return jmapSendRaw(account, session, mailAccount, submissionAccount, draftsID, identityID, cleanup, []byte(Message(account, draft)))
	}
	emailCreate, err := jmapEmailCreate(account, session, mailAccount, draftsID, draft)
	if err != nil {
		return err
	}
	body := jmapRequest{Using: []string{capMail, capSubmission}, MethodCalls: [][]any{
		{"Email/set", map[string]any{"accountId": mailAccount, "create": map[string]any{"draft": emailCreate}}, "e"},
		{"EmailSubmission/set", map[string]any{"accountId": submissionAccount, "create": map[string]any{"submission": map[string]any{"emailId": "#draft", "identityId": identityID}}, "onSuccessUpdateEmail": map[string]any{"#submission": cleanup}}, "s"},
	}}
	var response jmapResponse
	if err := httpJSON(account, "POST", session.APIURL, body, &response); err != nil {
		return err
	}
	if _, err := methodArgs(response, "e"); err != nil {
		return err
	}
	if _, err := methodArgs(response, "s"); err != nil {
		return err
	}
	return nil
}

func jmapSendRaw(account store.Account, session *jmapSession, mailAccount, submissionAccount, draftsID, identityID string, cleanup map[string]any, raw []byte) error {
	if session.UploadURL == "" {
		return fmt.Errorf("server did not advertise JMAP upload URL")
	}
	uploaded, err := jmapUpload(account, session.UploadURL, mailAccount, "message/rfc822", raw)
	if err != nil {
		return err
	}
	body := jmapRequest{Using: []string{capMail, capSubmission}, MethodCalls: [][]any{
		{"Email/import", map[string]any{"accountId": mailAccount, "emails": map[string]any{"draft": map[string]any{"blobId": uploaded.BlobID, "mailboxIds": map[string]bool{draftsID: true}, "keywords": map[string]bool{"$draft": true}}}}, "e"},
		{"EmailSubmission/set", map[string]any{"accountId": submissionAccount, "create": map[string]any{"submission": map[string]any{"emailId": "#draft", "identityId": identityID}}, "onSuccessUpdateEmail": map[string]any{"#submission": cleanup}}, "s"},
	}}
	var response jmapResponse
	if err := httpJSON(account, "POST", session.APIURL, body, &response); err != nil {
		return err
	}
	if _, err := methodArgs(response, "e"); err != nil {
		return err
	}
	if _, err := methodArgs(response, "s"); err != nil {
		return err
	}
	return nil
}

func jmapEmailCreate(account store.Account, session *jmapSession, mailAccount, draftsID string, draft Draft) (map[string]any, error) {
	textPart := map[string]any{"partId": "text", "type": "text/plain"}
	bodyStructure := any(textPart)
	if len(draft.Attachments) > 0 {
		if session.UploadURL == "" {
			return nil, fmt.Errorf("server did not advertise JMAP upload URL")
		}
		parts := []any{textPart}
		for i, attachment := range draft.Attachments {
			contentType := attachment.ContentType
			if strings.TrimSpace(contentType) == "" {
				contentType = "application/octet-stream"
			}
			uploaded, err := jmapUpload(account, session.UploadURL, mailAccount, contentType, attachment.Data)
			if err != nil {
				return nil, err
			}
			name := attachment.Filename
			if strings.TrimSpace(name) == "" {
				name = fmt.Sprintf("attachment-%d", i+1)
			}
			if uploaded.Type != "" {
				contentType = uploaded.Type
			}
			parts = append(parts, map[string]any{
				"blobId":      uploaded.BlobID,
				"type":        contentType,
				"size":        uploaded.Size,
				"name":        name,
				"disposition": "attachment",
			})
		}
		bodyStructure = map[string]any{"type": "multipart/mixed", "subParts": parts}
	}
	return map[string]any{
		"mailboxIds":    map[string]bool{draftsID: true},
		"keywords":      map[string]bool{"$draft": true},
		"from":          []map[string]string{jmapAddress(draftFrom(account, draft))},
		"to":            jmapRecipients(draft.To),
		"cc":            jmapRecipients(draft.Cc),
		"bcc":           jmapRecipients(draft.Bcc),
		"subject":       draft.Subject,
		"bodyStructure": bodyStructure,
		"bodyValues":    map[string]any{"text": map[string]any{"value": draft.Body, "isTruncated": false}},
	}, nil
}

func jmapAddress(value string) map[string]string {
	if addr, err := mail.ParseAddress(value); err == nil {
		out := map[string]string{"email": addr.Address}
		if addr.Name != "" {
			out["name"] = addr.Name
		}
		return out
	}
	return map[string]string{"email": value}
}

func jmapUpload(account store.Account, uploadURL, accountID, contentType string, data []byte) (jmapUploadResponse, error) {
	uploadURL = strings.ReplaceAll(uploadURL, "{accountId}", url.PathEscape(accountID))
	req, err := http.NewRequest("POST", uploadURL, bytes.NewReader(data))
	if err != nil {
		return jmapUploadResponse{}, err
	}
	req.Header.Set("Content-Type", contentType)
	setAuthHeader(req, account)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return jmapUploadResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return jmapUploadResponse{}, fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	var out jmapUploadResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return jmapUploadResponse{}, err
	}
	if out.BlobID == "" {
		return jmapUploadResponse{}, fmt.Errorf("JMAP upload response missing blobId")
	}
	return out, nil
}

func jmapIdentity(account store.Account, apiURL, submissionAccount, senderEmail string) (string, error) {
	body := jmapRequest{Using: []string{capSubmission}, MethodCalls: [][]any{{"Identity/get", map[string]any{"accountId": submissionAccount}, "i"}}}
	var response jmapResponse
	if err := httpJSON(account, "POST", apiURL, body, &response); err != nil {
		return "", err
	}
	args, err := methodArgs(response, "i")
	if err != nil {
		return "", err
	}
	items, _ := args["list"].([]any)
	for _, item := range items {
		identity, _ := item.(map[string]any)
		id := stringValue(identity["id"])
		if strings.EqualFold(stringValue(identity["email"]), senderEmail) && id != "" {
			return id, nil
		}
	}
	if len(items) > 0 {
		identity, _ := items[0].(map[string]any)
		if id := stringValue(identity["id"]); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("server did not advertise send identity")
}

func jmapMailboxes(account store.Account, apiURL, mailAccount string) (string, string, error) {
	boxes, err := jmapMailboxList(account, apiURL, mailAccount)
	if err != nil {
		return "", "", err
	}
	draftsID := ""
	sentID := ""
	for _, mailbox := range boxes {
		role := strings.ToLower(stringValue(mailbox["role"]))
		id := stringValue(mailbox["id"])
		if role == "drafts" {
			draftsID = id
		}
		if role == "sent" {
			sentID = id
		}
	}
	return draftsID, sentID, nil
}

func jmapMailboxList(account store.Account, apiURL, mailAccount string) ([]map[string]any, error) {
	body := jmapRequest{Using: []string{capMail}, MethodCalls: [][]any{{"Mailbox/get", map[string]any{"accountId": mailAccount, "ids": nil, "properties": []string{"id", "name", "role", "parentId"}}, "m"}}}
	var response jmapResponse
	if err := httpJSON(account, "POST", apiURL, body, &response); err != nil {
		return nil, err
	}
	args, err := methodArgs(response, "m")
	if err != nil {
		return nil, err
	}
	items, _ := args["list"].([]any)
	out := []map[string]any{}
	for _, item := range items {
		if mailbox, ok := item.(map[string]any); ok {
			out = append(out, mailbox)
		}
	}
	return out, nil
}

func fetchSession(account store.Account) (*jmapSession, error) {
	var session jmapSession
	if account.SessionURL == "" {
		return nil, fmt.Errorf("session_url missing")
	}
	if err := httpJSON(account, "GET", account.SessionURL, nil, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func jmapQuery(account store.Account, apiURL, mailAccount string, limit int) ([]string, error) {
	args := map[string]any{"accountId": mailAccount, "sort": []map[string]any{{"property": "receivedAt", "isAscending": false}}}
	if limit > 0 {
		args["limit"] = limit
	}
	body := jmapRequest{Using: []string{capMail}, MethodCalls: [][]any{{"Email/query", args, "q"}}}
	var response jmapResponse
	if err := httpJSON(account, "POST", apiURL, body, &response); err != nil {
		return nil, err
	}
	argsOut, err := methodArgs(response, "q")
	if err != nil {
		return nil, err
	}
	idsAny, _ := argsOut["ids"].([]any)
	ids := make([]string, 0, len(idsAny))
	for _, id := range idsAny {
		if value, ok := id.(string); ok {
			ids = append(ids, value)
		}
	}
	return ids, nil
}

func jmapGet(account store.Account, apiURL, mailAccount string, ids []string) ([]map[string]any, error) {
	idsAny := make([]any, 0, len(ids))
	for _, id := range ids {
		idsAny = append(idsAny, id)
	}
	body := jmapRequest{Using: []string{capMail}, MethodCalls: [][]any{{"Email/get", map[string]any{
		"accountId":           mailAccount,
		"ids":                 idsAny,
		"properties":          []string{"id", "receivedAt", "sentAt", "subject", "keywords", "mailboxIds", "from", "to", "cc", "bodyValues", "textBody", "htmlBody", "attachments", "hasAttachment"},
		"bodyProperties":      []string{"partId", "blobId", "size", "name", "type", "disposition", "cid"},
		"fetchTextBodyValues": true,
		"fetchHTMLBodyValues": true,
		"maxBodyValueBytes":   500000,
	}, "g"}}}
	var response jmapResponse
	if err := httpJSON(account, "POST", apiURL, body, &response); err != nil {
		return nil, err
	}
	argsOut, err := methodArgs(response, "g")
	if err != nil {
		return nil, err
	}
	listAny, _ := argsOut["list"].([]any)
	items := make([]map[string]any, 0, len(listAny))
	for _, item := range listAny {
		if value, ok := item.(map[string]any); ok {
			items = append(items, value)
		}
	}
	return items, nil
}

func methodArgs(response jmapResponse, callID string) (map[string]any, error) {
	for _, method := range response.MethodResponses {
		if len(method) != 3 {
			continue
		}
		var id string
		_ = json.Unmarshal(method[2], &id)
		if id != callID {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(method[1], &args); err != nil {
			return nil, err
		}
		return args, nil
	}
	return nil, fmt.Errorf("JMAP response missing call %s", callID)
}

func httpJSON(account store.Account, method, url string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	setAuthHeader(req, account)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func setAuthHeader(req *http.Request, account store.Account) {
	if strings.EqualFold(account.AuthKind, "basic") {
		token := base64.StdEncoding.EncodeToString([]byte(account.Username + ":" + account.Secret))
		req.Header.Set("Authorization", "Basic "+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+account.Secret)
	}
}

func jmapEmailToEML(account store.Account, session *jmapSession, mailAccount string, item map[string]any) (string, error) {
	body := jmapBody(item)
	attachments, err := jmapDownloadAttachments(account, session, mailAccount, item)
	if err != nil {
		return "", err
	}
	if len(attachments) > 0 {
		return jmapMultipartEmail(item, body, attachments), nil
	}
	return strings.Join(append(jmapEmailHeaders(item), "Content-Type: text/plain; charset=utf-8", "", body), "\r\n"), nil
}

func jmapEmailHeaders(item map[string]any) []string {
	date := cleanHeaderValue(stringValue(item["receivedAt"]))
	if date == "" {
		date = cleanHeaderValue(stringValue(item["sentAt"]))
	}
	if date == "" {
		date = time.Now().Format(time.RFC3339)
	}
	return []string{
		"From: " + cleanHeaderValue(firstAddress(item["from"])),
		"To: " + cleanHeaderValue(addresses(item["to"])),
		"Cc: " + cleanHeaderValue(addresses(item["cc"])),
		"Subject: " + cleanHeaderValue(stringValue(item["subject"])),
		"Date: " + date,
		"MIME-Version: 1.0",
		"X-Murat-JMAP-ID: " + cleanHeaderValue(stringValue(item["id"])),
	}
}

func jmapMultipartEmail(item map[string]any, body string, attachments []jmapAttachmentData) string {
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	headers := append(jmapEmailHeaders(item), "Content-Type: multipart/mixed; boundary="+writer.Boundary())
	out.WriteString(strings.Join(headers, "\r\n"))
	out.WriteString("\r\n\r\n")

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	textPart, _ := writer.CreatePart(textHeader)
	_, _ = textPart.Write([]byte(body))

	for i, attachment := range attachments {
		name := attachment.Filename
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("attachment-%d", i+1)
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
	return out.String()
}

func jmapDownloadAttachments(account store.Account, session *jmapSession, mailAccount string, item map[string]any) ([]jmapAttachmentData, error) {
	infos := jmapAttachmentInfos(item)
	if len(infos) == 0 {
		return nil, nil
	}
	if session == nil || strings.TrimSpace(session.DownloadURL) == "" {
		return nil, fmt.Errorf("JMAP message has attachments but session missing downloadUrl")
	}
	out := make([]jmapAttachmentData, 0, len(infos))
	for _, info := range infos {
		if strings.TrimSpace(info.BlobID) == "" {
			continue
		}
		data, err := jmapDownload(account, session.DownloadURL, mailAccount, info.BlobID, info.Filename, info.ContentType)
		if err != nil {
			return nil, err
		}
		info.Data = data
		out = append(out, info)
	}
	return out, nil
}

func jmapAttachmentInfos(item map[string]any) []jmapAttachmentData {
	items, _ := item["attachments"].([]any)
	out := []jmapAttachmentData{}
	for _, item := range items {
		part, _ := item.(map[string]any)
		name := stringValue(part["name"])
		if name == "" {
			name = stringValue(part["filename"])
		}
		contentType := stringValue(part["type"])
		out = append(out, jmapAttachmentData{Filename: name, ContentType: contentType, BlobID: stringValue(part["blobId"])})
	}
	return out
}

func jmapDownload(account store.Account, templateURL, accountID, blobID, name, contentType string) ([]byte, error) {
	name = cleanHeaderValue(name)
	contentType = cleanHeaderValue(contentType)
	if strings.TrimSpace(name) == "" {
		name = "attachment"
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	target := strings.ReplaceAll(templateURL, "{accountId}", url.PathEscape(accountID))
	target = strings.ReplaceAll(target, "{blobId}", url.PathEscape(blobID))
	target = strings.ReplaceAll(target, "{name}", url.PathEscape(name))
	target = strings.ReplaceAll(target, "{type}", url.PathEscape(contentType))
	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return nil, err
	}
	setAuthHeader(req, account)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return io.ReadAll(res.Body)
}

func jmapBody(item map[string]any) string {
	bodyValues, _ := item["bodyValues"].(map[string]any)
	for _, key := range []string{"textBody", "htmlBody"} {
		parts, _ := item[key].([]any)
		for _, part := range parts {
			partMap, _ := part.(map[string]any)
			partID := stringValue(partMap["partId"])
			valueMap, _ := bodyValues[partID].(map[string]any)
			value := stringValue(valueMap["value"])
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func jmapRead(item map[string]any) bool {
	keywords, _ := item["keywords"].(map[string]any)
	seen, _ := keywords["$seen"].(bool)
	return seen
}

func jmapHasAttachment(item map[string]any) bool {
	if value, ok := item["hasAttachment"].(bool); ok && value {
		return true
	}
	items, _ := item["attachments"].([]any)
	return len(items) > 0
}

func jmapTags(item map[string]any) []string {
	mailboxes, _ := item["mailboxIds"].(map[string]any)
	tags := []string{}
	for id := range mailboxes {
		if id != "" {
			tags = append(tags, id)
		}
	}
	if len(tags) == 0 {
		return []string{"INBOX"}
	}
	return tags
}

func jmapRecipients(value string) []map[string]string {
	out := []map[string]string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, map[string]string{"email": item})
		}
	}
	return out
}

func addresses(value any) string {
	items, _ := value.([]any)
	out := []string{}
	for _, item := range items {
		if addr := address(item); addr != "" {
			out = append(out, addr)
		}
	}
	return strings.Join(out, ", ")
}

func firstAddress(value any) string {
	items, _ := value.([]any)
	if len(items) == 0 {
		return ""
	}
	return address(items[0])
}

func address(value any) string {
	item, _ := value.(map[string]any)
	email := cleanHeaderValue(stringValue(item["email"]))
	name := cleanHeaderValue(stringValue(item["name"]))
	if name != "" && email != "" {
		return name + " <" + email + ">"
	}
	return email
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
