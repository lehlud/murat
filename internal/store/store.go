package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	htmlpkg "html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	cryptobox "lehnert.dev/murat/internal/crypto"
	"lehnert.dev/murat/internal/textutil"
)

type Store struct {
	paths       Paths
	box         *cryptobox.Box
	mu          sync.RWMutex
	index       Index
	search      map[string][]string
	dirty       bool
	searchDirty bool
	flushing    bool
}

type Index struct {
	Version        int                     `json:"version"`
	Mailboxes      map[string]any          `json:"mailboxes,omitempty"`
	Messages       map[string]*Message     `json:"messages"`
	Bodies         map[string]any          `json:"bodies,omitempty"`
	Drafts         []any                   `json:"drafts,omitempty"`
	SyncState      map[string]any          `json:"sync_state,omitempty"`
	KnownAddresses map[string]KnownAddress `json:"known_addresses,omitempty"`
	UpdatedAt      string                  `json:"updated_at,omitempty"`
}

type SearchIndex struct {
	Version int                 `json:"version"`
	Search  map[string][]string `json:"search_index"`
}

type Message struct {
	store          *Store `json:"-"`
	extra          map[string]json.RawMessage
	Key            string   `json:"key"`
	AccountID      string   `json:"account_id,omitempty"`
	From           string   `json:"from,omitempty"`
	To             []string `json:"to,omitempty"`
	Cc             []string `json:"cc,omitempty"`
	Subject        string   `json:"subject,omitempty"`
	ReceivedAt     string   `json:"received_at,omitempty"`
	SentAt         string   `json:"sent_at,omitempty"`
	Read           bool     `json:"read,omitempty"`
	ReadDirty      bool     `json:"read_dirty,omitempty"`
	Spam           bool     `json:"spam,omitempty"`
	Trashed        bool     `json:"trashed,omitempty"`
	FolderDirty    bool     `json:"folder_dirty,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	RemoteID       string   `json:"remote_id,omitempty"`
	HasAttachment  bool     `json:"has_attachment,omitempty"`
	ReportCategory string   `json:"report_category,omitempty"`
	ReportChecked  bool     `json:"report_checked,omitempty"`
	RawRel         string   `json:"eml_rel,omitempty"`
	BodyRel        string   `json:"body_rel,omitempty"`
	RawSize        int      `json:"raw_size,omitempty"`
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type alias Message
	var value alias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{"key", "account_id", "from", "to", "cc", "subject", "received_at", "sent_at", "read", "read_dirty", "spam", "trashed", "folder_dirty", "tags", "remote_id", "has_attachment", "report_category", "report_checked", "eml_rel", "body_rel", "raw_size"} {
		delete(raw, key)
	}
	if value.RawRel == "" {
		var legacy struct {
			RawRel string `json:"raw_rel"`
		}
		_ = json.Unmarshal(data, &legacy)
		value.RawRel = legacy.RawRel
		delete(raw, "raw_rel")
	}
	*m = Message(value)
	m.extra = raw
	return nil
}

func (m Message) MarshalJSON() ([]byte, error) {
	type alias Message
	value := alias(m)
	value.store = nil
	value.extra = nil
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for key, value := range m.extra {
		if _, exists := raw[key]; !exists {
			raw[key] = value
		}
	}
	return json.Marshal(raw)
}

type Body struct {
	Headers   string `json:"headers"`
	Text      string `json:"text"`
	RawSize   int    `json:"raw_size"`
	FetchedAt string `json:"fetched_at"`
}

type Attachment struct {
	Filename    string
	ContentType string
	Size        int
	Data        []byte
}

type DraftData struct {
	CreatedAt   string
	AccountID   string
	From        string
	To          string
	Cc          string
	Bcc         string
	Subject     string
	Body        string
	PGP         string
	Attachments []Attachment
}

type KnownAddress struct {
	Name   string `json:"name,omitempty"`
	Email  string `json:"email"`
	SeenAt string `json:"seen_at,omitempty"`
	Count  int    `json:"count,omitempty"`
}

func (a KnownAddress) String() string {
	name := strings.TrimSpace(a.Name)
	if name != "" {
		if !strings.ContainsAny(name, `()<>[]:;@\,."`) {
			return name + " <" + a.Email + ">"
		}
		return (&mail.Address{Name: name, Address: a.Email}).String()
	}
	return a.Email
}

type mailboxInfo struct {
	ID       string
	Name     string
	Role     string
	ParentID string
}

func Open(paths Paths, key []byte) (*Store, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	box, err := cryptobox.NewBox(key)
	if err != nil {
		return nil, err
	}
	s := &Store{paths: paths, box: box, index: emptyIndex(), search: map[string][]string{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	if err := s.loadSearch(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Paths() Paths { return s.paths }

func (s *Store) load() error {
	data, err := os.ReadFile(s.paths.IndexFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := s.box.Open(data)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(plain, &s.index); err != nil {
		return err
	}
	s.normalize()
	return nil
}

func (s *Store) loadSearch() error {
	data, err := os.ReadFile(s.paths.SearchFile)
	if os.IsNotExist(err) {
		s.search = s.buildSearchIndexLocked()
		s.searchDirty = true
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := s.box.Open(data)
	if err != nil {
		return err
	}
	var index SearchIndex
	if err := json.Unmarshal(plain, &index); err != nil {
		return err
	}
	s.search = normalizeSearchIndex(index.Search)
	s.updateDraftSearchLocked()
	if s.dirty {
		s.search = s.buildSearchIndexLocked()
		s.searchDirty = true
	}
	return nil
}

func emptyIndex() Index {
	return Index{Version: 1, Mailboxes: map[string]any{}, Messages: map[string]*Message{}, Bodies: map[string]any{}, Drafts: []any{}, SyncState: map[string]any{}, KnownAddresses: map[string]KnownAddress{}}
}

func (s *Store) normalize() {
	if s.index.Version == 0 {
		s.index.Version = 1
	}
	if s.index.Mailboxes == nil {
		s.index.Mailboxes = map[string]any{}
	}
	if s.index.Messages == nil {
		s.index.Messages = map[string]*Message{}
	}
	if s.index.Bodies == nil {
		s.index.Bodies = map[string]any{}
	}
	if s.index.Drafts == nil {
		s.index.Drafts = []any{}
	}
	if s.index.SyncState == nil {
		s.index.SyncState = map[string]any{}
	}
	if s.index.KnownAddresses == nil {
		s.index.KnownAddresses = map[string]KnownAddress{}
	}
	for _, msg := range s.index.Messages {
		msg.store = s
		if decoded := decodeHeader(msg.Subject); decoded != msg.Subject {
			msg.Subject = decoded
			s.dirty = true
		}
		if decoded := decodeAddressHeader(msg.From); decoded != msg.From {
			msg.From = decoded
			s.dirty = true
		}
		if decoded := decodeAddressHeaders(msg.To); !equalStringSlice(decoded, msg.To) {
			msg.To = decoded
			s.dirty = true
		}
		if decoded := decodeAddressHeaders(msg.Cc); !equalStringSlice(decoded, msg.Cc) {
			msg.Cc = decoded
			s.dirty = true
		}
	}
	if len(s.index.KnownAddresses) == 0 {
		for _, msg := range s.index.Messages {
			s.rememberMessageAddressesLocked(msg, messageTime(msg))
		}
	}
}

func (s *Store) Save() error {
	s.mu.Lock()
	s.normalize()
	s.index.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Unlock()
	s.mu.RLock()
	data, err := json.Marshal(s.index)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	sealed, err := s.box.Seal(data)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.paths.IndexFile, sealed, 0o600); err != nil {
		return err
	}
	return s.saveSearchIfDirty()
}

func (s *Store) saveSearchIfDirty() error {
	s.mu.RLock()
	dirty := s.searchDirty
	search := cloneSearchIndex(s.search)
	s.mu.RUnlock()
	if !dirty {
		return nil
	}
	plain, err := json.Marshal(SearchIndex{Version: 1, Search: search})
	if err != nil {
		return err
	}
	sealed, err := s.box.Seal(plain)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.paths.SearchFile, sealed, 0o600); err != nil {
		return err
	}
	s.mu.Lock()
	s.searchDirty = false
	s.mu.Unlock()
	return nil
}

func (s *Store) MarkDirty() {
	s.mu.Lock()
	s.dirty = true
	start := !s.flushing
	if start {
		s.flushing = true
	}
	s.mu.Unlock()
	if start {
		go s.flushLoop()
	}
}

func (s *Store) flushLoop() {
	time.Sleep(200 * time.Millisecond)
	for {
		s.mu.Lock()
		if !s.dirty {
			s.flushing = false
			s.mu.Unlock()
			return
		}
		s.dirty = false
		s.mu.Unlock()
		_ = s.Save()
	}
}

func (s *Store) Flush() error { return s.Save() }

func (s *Store) Messages(includeSpam bool) []*Message {
	return s.messagesFiltered(includeSpam, false, false)
}

func (s *Store) MessagesAll(includeSpam, includeSent bool) []*Message {
	return s.messagesFiltered(includeSpam, includeSent, true)
}

func (s *Store) MessagesCategory(category string) []*Message {
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		return nil
	}
	messages := s.messagesSnapshot()
	out := make([]*Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Trashed || msg.RawRel == "" || msg.IsSent() {
			continue
		}
		if s.ReportCategory(msg) != category {
			continue
		}
		out = append(out, msg)
	}
	sortMessages(out)
	return out
}

func (s *Store) messagesFiltered(includeSpam, includeSent, includeReports bool) []*Message {
	messages := s.messagesSnapshot()
	out := make([]*Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Trashed || (!includeSpam && msg.IsSpam()) || (!includeSent && msg.IsSent()) || (!includeReports && s.ReportCategory(msg) != "") || msg.RawRel == "" {
			continue
		}
		out = append(out, msg)
	}
	sortMessages(out)
	return out
}

func (s *Store) messagesSnapshot() []*Message {
	s.mu.RLock()
	out := make([]*Message, 0, len(s.index.Messages))
	for _, msg := range s.index.Messages {
		out = append(out, msg)
	}
	s.mu.RUnlock()
	return out
}

func (s *Store) Drafts() []*Message {
	s.mu.RLock()
	out := make([]*Message, 0, len(s.index.Drafts))
	for i, draft := range s.index.Drafts {
		msg := messageFromDraft(i, draft)
		if msg != nil {
			msg.store = s
			out = append(out, msg)
		}
	}
	s.mu.RUnlock()
	sortMessages(out)
	return out
}

func (s *Store) Trash() []*Message {
	messages := s.messagesSnapshot()
	out := make([]*Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Trashed && msg.RawRel != "" {
			out = append(out, msg)
		}
	}
	sortMessages(out)
	return out
}

func (s *Store) Search(query string, includeSpam, includeSent, includeDrafts bool) []*Message {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil
	}
	s.mu.RLock()
	var keys map[string]bool
	for _, token := range tokens {
		posting := s.search[token]
		if len(posting) == 0 {
			s.mu.RUnlock()
			return nil
		}
		current := map[string]bool{}
		for _, key := range posting {
			current[key] = true
		}
		if keys == nil {
			keys = current
			continue
		}
		for key := range keys {
			if !current[key] {
				delete(keys, key)
			}
		}
	}
	out := []*Message{}
	for key := range keys {
		if strings.HasPrefix(key, "draft:") {
			if includeDrafts {
				if msg := s.draftByKeyLocked(key); msg != nil {
					msg.store = s
					out = append(out, msg)
				}
			}
			continue
		}
		msg := s.index.Messages[key]
		if msg == nil || msg.Trashed || (!includeSpam && msg.IsSpam()) || (!includeSent && msg.IsSent()) || msg.RawRel == "" {
			continue
		}
		out = append(out, msg)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		left := messageTime(out[i])
		right := messageTime(out[j])
		if left != right {
			return left > right
		}
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func sortMessages(out []*Message) {
	sort.Slice(out, func(i, j int) bool {
		left := messageTime(out[i])
		right := messageTime(out[j])
		if left != right {
			return left > right
		}
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		return out[i].Key < out[j].Key
	})
}

func (s *Store) Message(key string) (*Message, bool) {
	s.mu.RLock()
	msg, ok := s.index.Messages[key]
	s.mu.RUnlock()
	return msg, ok
}

func (s *Store) KnownRemoteIDs(accountID string) map[string]bool {
	out := map[string]bool{}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, msg := range s.index.Messages {
		if msg == nil || msg.RemoteID == "" {
			continue
		}
		if accountID == "" || msg.AccountID == accountID {
			out[msg.RemoteID] = true
		}
	}
	return out
}

func (s *Store) MessagesForAccount(accountID string) []*Message {
	messages := s.messagesSnapshot()
	out := make([]*Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil || msg.RawRel == "" || msg.IsDraft() {
			continue
		}
		if accountID == "" || msg.AccountID == accountID {
			out = append(out, msg)
		}
	}
	sortMessages(out)
	return out
}

func (s *Store) RemoteMessage(accountID, remoteID string) (*Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, msg := range s.index.Messages {
		if msg == nil || msg.RemoteID == "" {
			continue
		}
		if msg.RemoteID == remoteID && (accountID == "" || msg.AccountID == accountID) {
			return msg, true
		}
	}
	return nil, false
}

func (s *Store) KnownAddresses() []KnownAddress {
	s.mu.RLock()
	out := make([]KnownAddress, 0, len(s.index.KnownAddresses))
	for _, addr := range s.index.KnownAddresses {
		out = append(out, addr)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].String()) < strings.ToLower(out[j].String())
	})
	return out
}

func (s *Store) ImportKnownAddresses(addresses []KnownAddress) {
	s.mu.Lock()
	changed := false
	for _, addr := range addresses {
		email := normalizeEmail(addr.Email)
		if email == "" {
			continue
		}
		current := s.index.KnownAddresses[email]
		if current.Email == "" {
			current.Email = email
			changed = true
		}
		if strings.TrimSpace(current.Name) == "" && strings.TrimSpace(addr.Name) != "" {
			current.Name = strings.TrimSpace(addr.Name)
			changed = true
		}
		if addr.SeenAt > current.SeenAt {
			current.SeenAt = addr.SeenAt
			changed = true
		}
		if addr.Count > current.Count {
			current.Count = addr.Count
			changed = true
		}
		s.index.KnownAddresses[email] = current
	}
	s.mu.Unlock()
	if changed {
		s.MarkDirty()
	}
}

func (s *Store) RememberAddressStrings(values ...string) {
	s.mu.Lock()
	for _, value := range values {
		s.rememberAddressListLocked(value, time.Now().UTC().Format(time.RFC3339))
	}
	s.mu.Unlock()
	s.MarkDirty()
}

func (s *Store) RemoveMessage(key string, removeEML bool) error {
	s.mu.Lock()
	msg := s.index.Messages[key]
	if msg == nil {
		s.mu.Unlock()
		return fmt.Errorf("message not found")
	}
	delete(s.index.Messages, key)
	delete(s.index.Bodies, key)
	removeSearchKey(s.search, key)
	s.searchDirty = true
	s.mu.Unlock()
	if removeEML && msg.RawRel != "" {
		_ = os.Remove(filepath.Join(s.paths.RawDir, msg.RawRel))
	}
	s.MarkDirty()
	return nil
}

func (s *Store) SetMailboxes(accountID string, boxes []map[string]any) {
	items := make([]any, 0, len(boxes))
	for _, box := range boxes {
		items = append(items, box)
	}
	s.mu.Lock()
	s.index.Mailboxes[accountID] = items
	s.mu.Unlock()
	s.MarkDirty()
}

func (s *Store) ImportRaw(raw []byte) (*Message, error) {
	parsed, bodyText, hasAttachment, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])
	rel := emlRelForKey(key)
	msg := messageFromMail(key, parsed, raw, rel, hasAttachment)
	if hasAttachment {
		msg.ReportCategory, msg.ReportChecked = reportCategoryFromRaw(raw)
	} else {
		msg.ReportChecked = true
	}
	if err := s.writeEncrypted(filepath.Join(s.paths.RawDir, rel), raw); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if existing := s.index.Messages[key]; existing != nil {
		if len(msg.Tags) == 0 {
			msg.Tags = append([]string(nil), existing.Tags...)
		}
		if msg.AccountID == "" {
			msg.AccountID = existing.AccountID
		}
		msg.Read = existing.Read
		msg.ReadDirty = existing.ReadDirty
		msg.Spam = existing.Spam
		msg.Trashed = existing.Trashed
		msg.FolderDirty = existing.FolderDirty
		if existing.ReportChecked && (existing.ReportCategory != "" || msg.ReportCategory == "") {
			msg.ReportCategory = existing.ReportCategory
			msg.ReportChecked = existing.ReportChecked
		}
	}
	msg.store = s
	s.index.Messages[key] = msg
	s.rememberHeaderAddressesLocked(parsed.Header, msg.ReceivedAt)
	s.updateSearchForKeyLocked(key, searchDocument(msg, bodyText))
	s.mu.Unlock()
	s.MarkDirty()
	return msg, nil
}

func (s *Store) ImportSent(accountID string, raw []byte) (*Message, error) {
	msg, err := s.ImportRaw(raw)
	if err != nil {
		return nil, err
	}
	msg.SetRemote(accountID, "")
	msg.SetRead(true)
	msg.SetTags([]string{"sent"})
	return msg, nil
}

func (s *Store) OpenBody(msg *Message) (*Body, error) {
	if msg != nil && msg.IsDraft() {
		body := s.DraftBody(msg)
		if body == nil {
			return nil, fmt.Errorf("draft not found")
		}
		return body, nil
	}
	if msg.RawRel == "" {
		return nil, fmt.Errorf("message has no raw EML")
	}
	raw, err := s.readEncrypted(filepath.Join(s.paths.RawDir, msg.RawRel))
	if err != nil {
		return nil, err
	}
	_, text, hasAttachment, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	changed := msg.HasAttachment != hasAttachment
	msg.HasAttachment = hasAttachment
	if changed {
		s.MarkDirty()
	}
	body := Body{Headers: headerText(raw), Text: text, RawSize: len(raw), FetchedAt: time.Now().UTC().Format(time.RFC3339)}
	return &body, nil
}

func (s *Store) Attachments(msg *Message) ([]Attachment, error) {
	if msg != nil && msg.IsDraft() {
		draft, ok := s.DraftData(msg)
		if !ok {
			return nil, fmt.Errorf("draft not found")
		}
		return cloneAttachments(draft.Attachments), nil
	}
	if msg == nil || msg.RawRel == "" {
		return nil, fmt.Errorf("message has no raw EML")
	}
	raw, err := s.readEncrypted(filepath.Join(s.paths.RawDir, msg.RawRel))
	if err != nil {
		return nil, err
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	attachments := []Attachment{}
	if err := extractAttachments(parsed.Header, parsed.Body, &attachments); err != nil {
		return nil, err
	}
	return attachments, nil
}

func (s *Store) RawMessage(msg *Message) ([]byte, error) {
	if msg == nil || msg.RawRel == "" {
		return nil, fmt.Errorf("message has no raw EML")
	}
	return s.readEncrypted(filepath.Join(s.paths.RawDir, msg.RawRel))
}

func (s *Store) SaveAttachments(msg *Message, dir string) ([]string, error) {
	attachments, err := s.Attachments(msg)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(attachments))
	for i, attachment := range attachments {
		name := attachment.Filename
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("attachment-%d", i+1)
		}
		path := uniquePath(filepath.Join(dir, safeAttachmentName(name)))
		if err := os.WriteFile(path, attachment.Data, 0o600); err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func (s *Store) readBodyRel(rel string) (*Body, error) {
	data, err := s.readEncrypted(filepath.Join(s.paths.BodyDir, rel))
	if err != nil {
		return nil, err
	}
	var body Body
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	return &body, nil
}

func (m *Message) SetRead(read bool) {
	if m.store == nil {
		if m.Read != read {
			m.ReadDirty = true
		}
		m.Read = read
		return
	}
	m.store.mu.Lock()
	if m.Read != read {
		m.ReadDirty = true
	}
	m.Read = read
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) SetReadSynced(read bool) {
	if m.store == nil {
		m.Read = read
		m.ReadDirty = false
		return
	}
	m.store.mu.Lock()
	m.Read = read
	m.ReadDirty = false
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) SetRemote(accountID, remoteID string) {
	if m.store == nil {
		m.AccountID = accountID
		m.RemoteID = remoteID
		return
	}
	m.store.mu.Lock()
	m.AccountID = accountID
	m.RemoteID = remoteID
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) MarkTrashed() {
	if m.store == nil {
		m.Trashed = true
		m.FolderDirty = true
		m.addTag("trash")
		return
	}
	m.store.mu.Lock()
	if !m.Trashed {
		m.FolderDirty = true
	}
	m.Trashed = true
	m.addTag("trash")
	m.store.updateSearchForMessageLocked(m)
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) SetSpam(spam bool) {
	currentSpam := m.IsSpam()
	if m.store == nil {
		if currentSpam != spam {
			m.FolderDirty = true
		}
		m.Spam = spam
		if spam {
			m.addTag("spam")
		} else {
			m.removeSpamTags()
		}
		return
	}
	m.store.mu.Lock()
	if currentSpam != spam {
		m.FolderDirty = true
	}
	m.Spam = spam
	if spam {
		m.addTag("spam")
	} else {
		m.removeSpamTags()
	}
	m.store.updateSearchForMessageLocked(m)
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) SetTags(tags []string) {
	clean := cleanTags(tags)
	if m.store == nil {
		if !equalStringSlice(m.Tags, clean) {
			m.FolderDirty = true
		}
		m.Tags = clean
		return
	}
	m.store.mu.Lock()
	if !equalStringSlice(m.Tags, clean) {
		m.FolderDirty = true
	}
	m.Tags = clean
	m.store.updateSearchForMessageLocked(m)
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) SetFolderSynced(tags []string, spam, trashed bool) {
	clean := cleanTags(tags)
	if m.store == nil {
		m.Tags = clean
		m.Spam = spam
		m.Trashed = trashed
		m.FolderDirty = false
		return
	}
	m.store.mu.Lock()
	m.Tags = clean
	m.Spam = spam
	m.Trashed = trashed
	m.FolderDirty = false
	m.store.updateSearchForMessageLocked(m)
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) ClearReadDirty() {
	if m.store == nil {
		m.ReadDirty = false
		return
	}
	m.store.mu.Lock()
	m.ReadDirty = false
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func (m *Message) ClearFolderDirty() {
	if m.store == nil {
		m.FolderDirty = false
		return
	}
	m.store.mu.Lock()
	m.FolderDirty = false
	m.store.mu.Unlock()
	m.store.MarkDirty()
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

func (m *Message) DisplayTags() []string {
	if len(m.Tags) > 0 {
		return m.resolveTagNames(m.Tags)
	}
	if tags := m.folderTags(); len(tags) > 0 {
		return m.resolveTagNames(tags)
	}
	if m.IsDraft() {
		return []string{"draft"}
	}
	if m.IsSent() {
		return []string{"sent"}
	}
	return []string{"INBOX"}
}

func (m *Message) FolderColumn() string {
	if m == nil {
		return ""
	}
	if m.IsDraft() {
		return "draft"
	}
	if cleanReportCategory(m.ReportCategory) == aggregateReportCategory {
		return aggregateReportCategory
	}
	candidates := m.folderTags()
	if len(candidates) == 0 {
		candidates = m.Tags
	}
	if len(candidates) == 0 && m.Spam {
		return "spam"
	}
	resolved := m.resolveMailboxInfos(candidates)
	for _, box := range resolved {
		switch normalizedMailboxRole(box.Role) {
		case "inbox":
			return "in"
		case "sent":
			return "out"
		case "junk", "spam":
			return "spam"
		case "drafts", "draft":
			return "draft"
		case "trash", "deleted":
			return "trash"
		}
	}
	for _, box := range resolved {
		if marker := folderMarkerFromName(box.Name); marker != "" {
			return marker
		}
	}
	for _, box := range resolved {
		if name := folderBaseName(box.Name); name != "" {
			return name
		}
	}
	if m.Spam {
		return "spam"
	}
	if m.IsSent() {
		return "out"
	}
	return "in"
}

func (m *Message) addTag(tag string) {
	for _, current := range m.Tags {
		if current == tag {
			return
		}
	}
	m.Tags = append(m.Tags, tag)
	sort.Strings(m.Tags)
}

func (m *Message) removeTag(tag string) {
	kept := m.Tags[:0]
	for _, current := range m.Tags {
		if current != tag {
			kept = append(kept, current)
		}
	}
	m.Tags = kept
}

func (m *Message) removeSpamTags() {
	kept := m.Tags[:0]
	for _, current := range m.Tags {
		if strings.EqualFold(strings.TrimSpace(current), "spam") || m.hasSpamTag([]string{current}) {
			continue
		}
		kept = append(kept, current)
	}
	m.Tags = kept
}

func (m *Message) IsSent() bool {
	if m == nil {
		return false
	}
	return hasSentTag(m.Tags) || hasSentTag(m.folderTags()) || m.hasSentMailbox(m.Tags) || m.hasSentMailbox(m.folderTags())
}

func (m *Message) IsSpam() bool {
	if m == nil {
		return false
	}
	return m.Spam || m.hasSpamTag(m.Tags) || m.hasSpamTag(m.folderTags())
}

func (m *Message) hasSpamTag(tags []string) bool {
	for _, box := range m.resolveMailboxInfos(tags) {
		switch normalizedMailboxRole(box.Role) {
		case "junk", "spam":
			return true
		}
		if folderMarkerFromName(box.Name) == "spam" {
			return true
		}
	}
	return false
}

func (m *Message) hasSentMailbox(tags []string) bool {
	for _, box := range m.resolveMailboxInfos(tags) {
		if normalizedMailboxRole(box.Role) == "sent" {
			return true
		}
		if folderMarkerFromName(box.Name) == "out" {
			return true
		}
	}
	return false
}

func hasSentTag(tags []string) bool {
	for _, tag := range tags {
		for _, part := range strings.Split(tag, "/") {
			name := strings.TrimSpace(strings.ToLower(part))
			if name == "sent" || name == "sent mail" || name == "sent messages" || name == "sent items" || name == "sent-mail" || name == "sentmail" || name == "outbox" || name == "gesendet" || name == "gesendete elemente" || name == "gesendete objekte" {
				return true
			}
		}
	}
	return false
}

func (m *Message) folderTags() []string {
	if m.extra == nil {
		return nil
	}
	var mailbox string
	if raw := m.extra["imap_mailbox"]; len(raw) > 0 && json.Unmarshal(raw, &mailbox) == nil && mailbox != "" {
		return []string{mailbox}
	}
	var mailboxes []string
	if raw := m.extra["mailbox_ids"]; len(raw) > 0 && json.Unmarshal(raw, &mailboxes) == nil && len(mailboxes) > 0 {
		return mailboxes
	}
	return nil
}

func (m *Message) resolveTagNames(values []string) []string {
	infos := m.resolveMailboxInfos(values)
	out := []string{}
	for _, info := range infos {
		if name := folderBaseName(info.Name); name != "" {
			out = append(out, name)
		}
	}
	return cleanTags(out)
}

func (m *Message) resolveMailboxInfos(values []string) []mailboxInfo {
	out := []mailboxInfo{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if m.store != nil {
			if info, ok := m.store.mailboxInfo(m.AccountID, value); ok {
				out = append(out, info)
				continue
			}
		}
		out = append(out, mailboxInfo{ID: value, Name: value})
	}
	return out
}

func (s *Store) mailboxInfo(accountID, mailboxID string) (mailboxInfo, bool) {
	if s == nil || mailboxID == "" {
		return mailboxInfo{}, false
	}
	boxes := s.mailboxInfos(accountID)
	byID := map[string]mailboxInfo{}
	for _, box := range boxes {
		byID[box.ID] = box
	}
	box, ok := byID[mailboxID]
	if !ok {
		return mailboxInfo{}, false
	}
	box.Name = s.mailboxPath(box.ID, byID, map[string]bool{})
	return box, true
}

func (s *Store) mailboxInfos(accountID string) []mailboxInfo {
	value := s.index.Mailboxes[accountID]
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := []mailboxInfo{}
	for _, item := range items {
		box, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringAny(box["id"])
		if id == "" {
			continue
		}
		out = append(out, mailboxInfo{ID: id, Name: firstNonEmptyString(stringAny(box["name"]), id), Role: stringAny(box["role"]), ParentID: stringAny(box["parentId"])})
	}
	return out
}

func (s *Store) mailboxPath(id string, byID map[string]mailboxInfo, seen map[string]bool) string {
	if seen[id] {
		return id
	}
	seen[id] = true
	box, ok := byID[id]
	if !ok {
		return id
	}
	name := firstNonEmptyString(box.Name, id)
	if box.ParentID != "" {
		if _, ok := byID[box.ParentID]; ok {
			return s.mailboxPath(box.ParentID, byID, seen) + "/" + name
		}
	}
	return name
}

func normalizedMailboxRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "junk" || role == "spam" || role == "trash" || role == "deleted" {
		return role
	}
	return role
}

func folderMarkerFromName(name string) string {
	base := strings.ToLower(folderBaseName(name))
	switch base {
	case "inbox", "posteingang":
		return "in"
	case "sent", "sent mail", "sent messages", "sent items", "sent-mail", "sentmail", "outbox", "gesendet", "gesendete elemente", "gesendete objekte":
		return "out"
	case "spam", "junk", "junk e-mail", "junk mail", "bulk mail":
		return "spam"
	case "drafts", "draft", "entwürfe", "entwuerfe":
		return "draft"
	case "trash", "bin", "deleted", "deleted items", "gelöscht", "geloescht", "gelöschte elemente", "geloeschte elemente", "gelöschte objekte", "geloeschte objekte", "papierkorb":
		return "trash"
	case "archive", "archiv":
		return "arch"
	default:
		return ""
	}
}

func folderBaseName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	parts := strings.Split(name, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if part := strings.TrimSpace(parts[i]); part != "" {
			return part
		}
	}
	return name
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (m *Message) IsDraft() bool { return strings.HasPrefix(m.Key, "draft:") }

func (s *Store) SaveDraft(accountID, from, to, cc, bcc, subject, body string) error {
	_, err := s.SaveDraftData(DraftData{AccountID: accountID, From: from, To: to, Cc: cc, Bcc: bcc, Subject: subject, Body: body})
	return err
}

func (s *Store) SaveDraftData(draft DraftData) (*Message, error) {
	if strings.TrimSpace(draft.CreatedAt) == "" {
		draft.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	record := draftRecord(draft)
	s.mu.Lock()
	s.index.Drafts = append(s.index.Drafts, record)
	key := fmt.Sprintf("draft:%d", len(s.index.Drafts)-1)
	msg := messageFromDraft(len(s.index.Drafts)-1, record)
	if msg != nil {
		msg.store = s
		s.updateSearchForKeyLocked(key, searchDocument(msg, draft.Body))
	}
	s.mu.Unlock()
	s.MarkDirty()
	return msg, nil
}

func (s *Store) UpdateDraft(key string, draft DraftData) (*Message, error) {
	index, ok := draftIndex(key)
	if !ok {
		return nil, fmt.Errorf("draft not found")
	}
	s.mu.Lock()
	if index < 0 || index >= len(s.index.Drafts) {
		s.mu.Unlock()
		return nil, fmt.Errorf("draft not found")
	}
	current, ok := draftDataFromAny(s.index.Drafts[index])
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("draft not found")
	}
	if strings.TrimSpace(draft.CreatedAt) == "" {
		draft.CreatedAt = current.CreatedAt
	}
	if strings.TrimSpace(draft.CreatedAt) == "" {
		draft.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	record := draftRecord(draft)
	s.index.Drafts[index] = record
	msg := messageFromDraft(index, record)
	if msg != nil {
		msg.store = s
		s.updateSearchForKeyLocked(key, searchDocument(msg, draft.Body))
	}
	s.mu.Unlock()
	s.MarkDirty()
	return msg, nil
}

func (s *Store) DeleteDraft(key string) error {
	index, ok := draftIndex(key)
	if !ok {
		return fmt.Errorf("draft not found")
	}
	s.mu.Lock()
	if index < 0 || index >= len(s.index.Drafts) {
		s.mu.Unlock()
		return fmt.Errorf("draft not found")
	}
	if _, ok := draftDataFromAny(s.index.Drafts[index]); !ok {
		s.mu.Unlock()
		return fmt.Errorf("draft not found")
	}
	s.index.Drafts[index] = nil
	removeSearchKey(s.search, key)
	s.searchDirty = true
	s.mu.Unlock()
	s.MarkDirty()
	return nil
}

func (s *Store) DraftData(msg *Message) (DraftData, bool) {
	if msg == nil || !msg.IsDraft() {
		return DraftData{}, false
	}
	s.mu.RLock()
	data, ok := s.draftDataByKeyLocked(msg.Key)
	s.mu.RUnlock()
	return data, ok
}

func messageFromDraft(index int, value any) *Message {
	draft, ok := draftDataFromAny(value)
	if !ok {
		return nil
	}
	to := singleStringSlice(draft.To)
	cc := singleStringSlice(draft.Cc)
	return &Message{
		Key:           fmt.Sprintf("draft:%d", index),
		AccountID:     draft.AccountID,
		From:          firstNonEmptyString(draft.From, "draft"),
		To:            to,
		Cc:            cc,
		Subject:       draft.Subject,
		ReceivedAt:    draft.CreatedAt,
		SentAt:        draft.CreatedAt,
		Read:          true,
		Tags:          []string{"draft"},
		HasAttachment: len(draft.Attachments) > 0,
		extra: map[string]json.RawMessage{
			"body": rawJSON(draft.Body),
		},
	}
}

func (s *Store) draftByKeyLocked(key string) *Message {
	index, ok := draftIndex(key)
	if !ok || index < 0 || index >= len(s.index.Drafts) {
		return nil
	}
	return messageFromDraft(index, s.index.Drafts[index])
}

func (s *Store) draftDataByKeyLocked(key string) (DraftData, bool) {
	index, ok := draftIndex(key)
	if !ok || index < 0 || index >= len(s.index.Drafts) {
		return DraftData{}, false
	}
	return draftDataFromAny(s.index.Drafts[index])
}

func draftIndex(key string) (int, bool) {
	indexText := strings.TrimPrefix(key, "draft:")
	if indexText == key {
		return 0, false
	}
	index, err := strconv.Atoi(indexText)
	if err != nil {
		return 0, false
	}
	return index, true
}

func (s *Store) DraftBody(msg *Message) *Body {
	if msg == nil || !msg.IsDraft() {
		return nil
	}
	s.mu.RLock()
	draft, ok := s.draftDataByKeyLocked(msg.Key)
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	return &Body{Headers: draftHeaders(draft), Text: draft.Body, FetchedAt: time.Now().UTC().Format(time.RFC3339)}
}

func draftRecord(draft DraftData) map[string]any {
	record := map[string]any{
		"created_at": draft.CreatedAt,
		"account_id": draft.AccountID,
		"from":       draft.From,
		"to":         draft.To,
		"cc":         draft.Cc,
		"bcc":        draft.Bcc,
		"subject":    draft.Subject,
		"body":       draft.Body,
		"pgp":        draft.PGP,
	}
	if len(draft.Attachments) > 0 {
		record["attachments"] = draftAttachmentRecords(draft.Attachments)
	}
	return record
}

func draftDataFromAny(value any) (DraftData, bool) {
	record, ok := value.(map[string]any)
	if !ok {
		return DraftData{}, false
	}
	return DraftData{
		CreatedAt:   stringAny(record["created_at"]),
		AccountID:   stringAny(record["account_id"]),
		From:        stringAny(record["from"]),
		To:          stringAny(record["to"]),
		Cc:          stringAny(record["cc"]),
		Bcc:         stringAny(record["bcc"]),
		Subject:     stringAny(record["subject"]),
		Body:        stringAny(record["body"]),
		PGP:         stringAny(record["pgp"]),
		Attachments: draftAttachmentsFromAny(record["attachments"]),
	}, true
}

func draftHeaders(draft DraftData) string {
	lines := []string{
		"From: " + draft.From,
		"To: " + draft.To,
		"Cc: " + draft.Cc,
		"Bcc: " + draft.Bcc,
		"Subject: " + draft.Subject,
	}
	if draft.CreatedAt != "" {
		lines = append(lines, "Date: "+draft.CreatedAt)
	}
	return strings.Join(lines, "\n")
}

func draftAttachmentRecords(attachments []Attachment) []map[string]any {
	out := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		data := append([]byte(nil), attachment.Data...)
		size := attachment.Size
		if size == 0 {
			size = len(data)
		}
		out = append(out, map[string]any{
			"filename":     attachment.Filename,
			"content_type": attachment.ContentType,
			"size":         size,
			"data":         data,
		})
	}
	return out
}

func draftAttachmentsFromAny(value any) []Attachment {
	switch typed := value.(type) {
	case []Attachment:
		return cloneAttachments(typed)
	case []map[string]any:
		out := make([]Attachment, 0, len(typed))
		for _, item := range typed {
			if attachment, ok := draftAttachmentFromMap(item); ok {
				out = append(out, attachment)
			}
		}
		return out
	case []any:
		out := make([]Attachment, 0, len(typed))
		for _, item := range typed {
			if record, ok := item.(map[string]any); ok {
				if attachment, ok := draftAttachmentFromMap(record); ok {
					out = append(out, attachment)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func draftAttachmentFromMap(record map[string]any) (Attachment, bool) {
	data := bytesAny(record["data"])
	size := intAny(record["size"])
	if size == 0 {
		size = len(data)
	}
	attachment := Attachment{Filename: stringAny(record["filename"]), ContentType: stringAny(record["content_type"]), Size: size, Data: data}
	return attachment, attachment.Filename != "" || attachment.ContentType != "" || len(attachment.Data) > 0
}

func cloneAttachments(attachments []Attachment) []Attachment {
	out := make([]Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachment.Data = append([]byte(nil), attachment.Data...)
		if attachment.Size == 0 {
			attachment.Size = len(attachment.Data)
		}
		out = append(out, attachment)
	}
	return out
}

func singleStringSlice(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{value}
}

func rawJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func stringAny(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func intAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		value, _ := typed.Int64()
		return int(value)
	default:
		return 0
	}
}

func bytesAny(value any) []byte {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...)
	case string:
		decoded, err := base64.StdEncoding.DecodeString(typed)
		if err == nil {
			return decoded
		}
		return []byte(typed)
	default:
		return nil
	}
}

func splitAnyStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []any:
		out := []string{}
		for _, item := range typed {
			if text := stringAny(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

var tokenRE = regexp.MustCompile(`[a-z0-9][a-z0-9_.+-]{1,}`)

func tokenize(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, token := range tokenRE.FindAllString(strings.ToLower(text), -1) {
		if !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}
	return out
}

func (s *Store) buildSearchIndexLocked() map[string][]string {
	index := map[string][]string{}
	for key, msg := range s.index.Messages {
		if msg == nil {
			continue
		}
		addSearchDoc(index, key, s.searchDocumentForMessageLocked(msg))
	}
	for i, draft := range s.index.Drafts {
		msg := messageFromDraft(i, draft)
		if msg == nil {
			continue
		}
		body := ""
		if raw := msg.extra["body"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		addSearchDoc(index, msg.Key, searchDocument(msg, body))
	}
	return normalizeSearchIndex(index)
}

func (s *Store) updateDraftSearchLocked() {
	for token, keys := range s.search {
		kept := keys[:0]
		for _, key := range keys {
			if !strings.HasPrefix(key, "draft:") {
				kept = append(kept, key)
			}
		}
		if len(kept) == 0 {
			delete(s.search, token)
		} else {
			s.search[token] = kept
		}
	}
	for i, draft := range s.index.Drafts {
		msg := messageFromDraft(i, draft)
		if msg == nil {
			continue
		}
		body := ""
		if raw := msg.extra["body"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		addSearchDoc(s.search, msg.Key, searchDocument(msg, body))
	}
	s.search = normalizeSearchIndex(s.search)
	s.searchDirty = true
}

func (s *Store) updateSearchForMessageLocked(msg *Message) {
	if msg == nil {
		return
	}
	s.updateSearchForKeyLocked(msg.Key, s.searchDocumentForMessageLocked(msg))
}

func (s *Store) updateSearchForKeyLocked(key, doc string) {
	removeSearchKey(s.search, key)
	addSearchDoc(s.search, key, doc)
	s.search = normalizeSearchIndex(s.search)
	s.searchDirty = true
}

func (s *Store) searchDocumentForMessageLocked(msg *Message) string {
	body := ""
	if value, ok := s.index.Bodies[msg.Key].(map[string]any); ok {
		body = stringAny(value["body"])
	}
	return searchDocument(msg, body)
}

func searchDocument(msg *Message, body string) string {
	return strings.Join([]string{
		msg.Subject,
		msg.From,
		strings.Join(msg.To, " "),
		strings.Join(msg.Cc, " "),
		strings.Join(msg.Tags, " "),
		body,
	}, "\n")
}

func addSearchDoc(index map[string][]string, key, doc string) {
	for _, token := range tokenize(doc) {
		index[token] = append(index[token], key)
	}
}

func removeSearchKey(index map[string][]string, key string) {
	for token, keys := range index {
		kept := keys[:0]
		for _, current := range keys {
			if current != key {
				kept = append(kept, current)
			}
		}
		if len(kept) == 0 {
			delete(index, token)
		} else {
			index[token] = kept
		}
	}
}

func normalizeSearchIndex(value map[string][]string) map[string][]string {
	out := map[string][]string{}
	for token, keys := range value {
		if token == "" {
			continue
		}
		seen := map[string]bool{}
		clean := []string{}
		for _, key := range keys {
			if key != "" && !seen[key] {
				seen[key] = true
				clean = append(clean, key)
			}
		}
		if len(clean) > 0 {
			sort.Strings(clean)
			out[token] = clean
		}
	}
	return out
}

func cloneSearchIndex(value map[string][]string) map[string][]string {
	out := map[string][]string{}
	for token, keys := range value {
		out[token] = append([]string(nil), keys...)
	}
	return out
}

func (s *Store) writeEncrypted(path string, data []byte) error {
	sealed, err := s.box.Seal(data)
	if err != nil {
		return err
	}
	return atomicWrite(path, sealed, 0o600)
}

func (s *Store) readEncrypted(path string) ([]byte, error) {
	sealed, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return s.box.Open(sealed)
}

func parseRaw(raw []byte) (*mail.Message, string, bool, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, "", false, err
	}
	body, hasAttachment, err := extractMailText(msg.Header, msg.Body)
	if err != nil {
		return nil, "", false, err
	}
	return msg, strings.TrimSpace(body), hasAttachment, nil
}

func messageFromMail(key string, parsed *mail.Message, raw []byte, rel string, hasAttachment bool) *Message {
	date := parsed.Header.Get("Date")
	if parsedDate, err := mail.ParseDate(date); err == nil {
		date = parsedDate.UTC().Format(time.RFC3339)
	}
	tags := []string{}
	if rawTags := parsed.Header.Get("X-Murat-Tags"); rawTags != "" {
		for _, tag := range strings.Split(rawTags, ",") {
			if strings.TrimSpace(tag) != "" {
				tags = append(tags, strings.TrimSpace(tag))
			}
		}
	}
	return &Message{
		Key:           key,
		From:          decodeAddressHeader(parsed.Header.Get("From")),
		To:            splitAddressHeader(parsed.Header.Get("To")),
		Cc:            splitAddressHeader(parsed.Header.Get("Cc")),
		Subject:       decodeHeader(parsed.Header.Get("Subject")),
		ReceivedAt:    date,
		RemoteID:      remoteIDFromHeader(parsed.Header),
		Read:          false,
		Tags:          cleanTags(tags),
		RawRel:        rel,
		RawSize:       len(raw),
		HasAttachment: hasAttachment,
	}
}

func decodeHeader(value string) string {
	decoder := mime.WordDecoder{CharsetReader: charsetReader}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return textutil.CleanHeaderValue(value)
	}
	return textutil.CleanHeaderValue(decoded)
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(decodeBytes(data, charset)), nil
}

func remoteIDFromHeader(header mail.Header) string {
	if value := textutil.CleanHeaderValue(header.Get("X-Murat-Remote-ID")); value != "" {
		return value
	}
	if value := textutil.CleanHeaderValue(header.Get("X-Murat-JMAP-ID")); value != "" {
		return "jmap:" + value
	}
	return ""
}

func (s *Store) rememberMessageAddressesLocked(msg *Message, seenAt string) {
	if msg == nil {
		return
	}
	s.rememberAddressListLocked(msg.From, seenAt)
	for _, value := range msg.To {
		s.rememberAddressListLocked(value, seenAt)
	}
	for _, value := range msg.Cc {
		s.rememberAddressListLocked(value, seenAt)
	}
}

func (s *Store) rememberHeaderAddressesLocked(header mail.Header, seenAt string) {
	for _, key := range []string{"From", "Sender", "Reply-To", "To", "Cc", "Bcc"} {
		s.rememberAddressListLocked(header.Get(key), seenAt)
	}
}

func (s *Store) rememberAddressListLocked(value, seenAt string) {
	for _, addr := range parseKnownAddresses(value) {
		key := strings.ToLower(addr.Email)
		if key == "" {
			continue
		}
		current := s.index.KnownAddresses[key]
		if current.Email == "" {
			current.Email = addr.Email
		}
		if strings.TrimSpace(current.Name) == "" && strings.TrimSpace(addr.Name) != "" {
			current.Name = addr.Name
		}
		if seenAt != "" {
			current.SeenAt = seenAt
		}
		current.Count++
		s.index.KnownAddresses[key] = current
	}
}

var emailAddressRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+`)

func parseKnownAddresses(value string) []KnownAddress {
	value = strings.TrimSpace(decodeHeader(value))
	if value == "" {
		return nil
	}
	parser := mail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charsetReader}}
	parsed, err := parser.ParseList(value)
	if err == nil {
		out := make([]KnownAddress, 0, len(parsed))
		for _, addr := range parsed {
			if email := normalizeEmail(addr.Address); email != "" {
				out = append(out, KnownAddress{Name: strings.TrimSpace(addr.Name), Email: email})
			}
		}
		return out
	}
	seen := map[string]bool{}
	out := []KnownAddress{}
	for _, email := range emailAddressRE.FindAllString(value, -1) {
		email = normalizeEmail(email)
		if email != "" && !seen[email] {
			seen[email] = true
			out = append(out, KnownAddress{Email: email})
		}
	}
	return out
}

func normalizeEmail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(value); err == nil {
		value = addr.Address
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func emlRelForKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	hexValue := hex.EncodeToString(sum[:])
	return filepath.Join(hexValue[:2], hexValue[2:]+".eml.enc")
}

func extractMailText(header mail.Header, body io.Reader) (string, bool, error) {
	contentType := header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", false, nil
		}
		plain, html, hasAttachment, err := extractMultipart(multipart.NewReader(body, boundary))
		if err != nil {
			return "", hasAttachment, err
		}
		if strings.TrimSpace(plain) != "" {
			return plain, hasAttachment, nil
		}
		return htmlToRichText(html), hasAttachment, nil
	}
	data, err := readDecoded(body, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(contentType) == "" {
		if text, hasAttachment, ok, err := extractEmbeddedMIMEText(data); ok || err != nil {
			return text, hasAttachment, err
		}
	}
	return decodeTextBody(data, params["charset"], mediaType), false, nil
}

func extractEmbeddedMIMEText(data []byte) (string, bool, bool, error) {
	trimmed := bytes.TrimLeft(data, "\xef\xbb\xbf \t\r\n")
	if !startsWithMIMEHeader(trimmed) {
		return "", false, false, nil
	}
	msg, err := mail.ReadMessage(bytes.NewReader(trimmed))
	if err != nil {
		return "", false, false, nil
	}
	if strings.TrimSpace(msg.Header.Get("Content-Type")) == "" {
		return "", false, false, nil
	}
	text, hasAttachment, err := extractMailText(msg.Header, msg.Body)
	return text, hasAttachment, true, err
}

func startsWithMIMEHeader(data []byte) bool {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	line = bytes.TrimSpace(line)
	return bytes.HasPrefix(bytes.ToLower(line), []byte("content-type:"))
}

func extractMultipart(reader *multipart.Reader) (string, string, bool, error) {
	var plain []string
	var html []string
	hasAttachment := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", hasAttachment, err
		}
		contentType := part.Header.Get("Content-Type")
		mediaType, params, _ := mime.ParseMediaType(contentType)
		disposition, _, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if attachmentPart(mail.Header(part.Header), disposition, mediaType, partFilename(part, params)) {
			hasAttachment = true
			continue
		}
		if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
			boundary := params["boundary"]
			if boundary == "" {
				continue
			}
			nestedPlain, nestedHTML, nestedAttachment, err := extractMultipart(multipart.NewReader(part, boundary))
			if err != nil {
				return "", "", hasAttachment, err
			}
			if nestedAttachment {
				hasAttachment = true
			}
			if nestedPlain != "" {
				plain = append(plain, nestedPlain)
			}
			if nestedHTML != "" {
				html = append(html, nestedHTML)
			}
			continue
		}
		data, err := readDecoded(part, part.Header.Get("Content-Transfer-Encoding"))
		if err != nil {
			return "", "", hasAttachment, err
		}
		switch strings.ToLower(mediaType) {
		case "text/plain":
			plain = append(plain, decodeTextBody(data, params["charset"], mediaType))
		case "text/html":
			html = append(html, decodeBytes(repairMissingQuotedPrintable(data), params["charset"]))
		}
	}
	return strings.Join(plain, "\n\n"), strings.Join(html, "\n\n"), hasAttachment, nil
}

func decodeTextBody(data []byte, charset, mediaType string) string {
	data = repairMissingQuotedPrintable(data)
	text := decodeBytes(data, charset)
	if strings.HasPrefix(strings.ToLower(mediaType), "text/html") || looksLikeHTMLDocument(text) {
		return htmlToRichText(text)
	}
	return text
}

func repairMissingQuotedPrintable(data []byte) []byte {
	if !looksLikeQuotedPrintable(data) {
		return data
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
	if err != nil || len(decoded) == 0 {
		return data
	}
	return decoded
}

func looksLikeQuotedPrintable(data []byte) bool {
	hexEscapes := 0
	softBreaks := 0
	for i := 0; i < len(data)-1; i++ {
		if data[i] != '=' {
			continue
		}
		if data[i+1] == '\n' {
			softBreaks++
			i++
			continue
		}
		if i+2 < len(data) && data[i+1] == '\r' && data[i+2] == '\n' {
			softBreaks++
			i += 2
			continue
		}
		if i+2 < len(data) && isHex(data[i+1]) && isHex(data[i+2]) {
			hexEscapes++
			i += 2
		}
	}
	return (softBreaks > 0 && hexEscapes > 0) || softBreaks >= 2 || hexEscapes >= 3
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func looksLikeHTMLDocument(text string) bool {
	text = strings.TrimLeft(text, "\ufeff \t\r\n")
	text = strings.ToLower(text)
	return strings.HasPrefix(text, "<!doctype html") || strings.HasPrefix(text, "<html") || strings.HasPrefix(text, "<head") || strings.HasPrefix(text, "<body")
}

func extractAttachments(header mail.Header, body io.Reader, out *[]Attachment) error {
	contentType := header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil
		}
		reader := multipart.NewReader(body, boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			partType, typeParams, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			disposition, _, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			filename := partFilename(part, typeParams)
			if attachmentPart(mail.Header(part.Header), disposition, partType, filename) {
				data, err := readDecoded(part, part.Header.Get("Content-Transfer-Encoding"))
				if err != nil {
					return err
				}
				if strings.TrimSpace(filename) == "" {
					filename = inlineAttachmentName(mail.Header(part.Header), partType, len(*out)+1)
				}
				*out = append(*out, Attachment{Filename: filename, ContentType: partType, Size: len(data), Data: data})
				continue
			}
			if strings.HasPrefix(strings.ToLower(partType), "multipart/") {
				if err := extractAttachments(mail.Header(part.Header), part, out); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func partFilename(part *multipart.Part, typeParams map[string]string) string {
	if filename := strings.TrimSpace(part.FileName()); filename != "" {
		return filename
	}
	return strings.TrimSpace(typeParams["name"])
}

func attachmentPart(header mail.Header, disposition, mediaType, filename string) bool {
	if strings.EqualFold(disposition, "attachment") || strings.TrimSpace(filename) != "" {
		return true
	}
	if !inlineImagePart(mediaType) {
		return false
	}
	return strings.EqualFold(disposition, "inline") || strings.TrimSpace(header.Get("Content-ID")) != "" || strings.TrimSpace(header.Get("X-Attachment-Id")) != ""
}

func inlineImagePart(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return strings.HasPrefix(mediaType, "image/")
}

func inlineAttachmentName(header mail.Header, mediaType string, index int) string {
	name := strings.Trim(strings.TrimSpace(header.Get("Content-ID")), "<>")
	if name == "" {
		name = strings.TrimSpace(header.Get("X-Attachment-Id"))
	}
	name = safeAttachmentName(name)
	if name == "attachment" {
		name = fmt.Sprintf("inline-%d", index)
	}
	if filepath.Ext(name) == "" {
		if exts, _ := mime.ExtensionsByType(mediaType); len(exts) > 0 {
			name += exts[0]
		}
	}
	return name
}

func readDecoded(reader io.Reader, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return io.ReadAll(base64.NewDecoder(base64.StdEncoding, reader))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(reader))
	default:
		return io.ReadAll(reader)
	}
}

func decodeBytes(data []byte, charset string) string {
	charset = strings.ToLower(strings.TrimSpace(charset))
	switch charset {
	case "", "utf-8", "us-ascii":
		if !utf8.Valid(data) {
			return decodeWindows1252(data)
		}
		return string(data)
	case "iso-8859-1", "latin1", "latin-1":
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return string(runes)
	case "iso-8859-2", "latin2", "latin-2":
		return decodeISO88592(data)
	case "windows-1252", "cp1252":
		return decodeWindows1252(data)
	default:
		return string(data)
	}
}

func decodeISO88592(data []byte) string {
	table := map[byte]rune{
		0xA1: 'Ą', 0xA2: '˘', 0xA3: 'Ł', 0xA5: 'Ľ', 0xA6: 'Ś', 0xA9: 'Š', 0xAA: 'Ş', 0xAB: 'Ť', 0xAC: 'Ź', 0xAE: 'Ž', 0xAF: 'Ż',
		0xB1: 'ą', 0xB2: '˛', 0xB3: 'ł', 0xB5: 'ľ', 0xB6: 'ś', 0xB7: 'ˇ', 0xB9: 'š', 0xBA: 'ş', 0xBB: 'ť', 0xBC: 'ź', 0xBD: '˝', 0xBE: 'ž', 0xBF: 'ż',
		0xC0: 'Ŕ', 0xC3: 'Ă', 0xC5: 'Ĺ', 0xC6: 'Ć', 0xC8: 'Č', 0xCA: 'Ę', 0xCC: 'Ě', 0xCF: 'Ď', 0xD0: 'Đ', 0xD1: 'Ń', 0xD2: 'Ň', 0xD5: 'Ő', 0xD8: 'Ř', 0xD9: 'Ů', 0xDB: 'Ű', 0xDE: 'Ţ',
		0xE0: 'ŕ', 0xE3: 'ă', 0xE5: 'ĺ', 0xE6: 'ć', 0xE8: 'č', 0xEA: 'ę', 0xEC: 'ě', 0xEF: 'ď', 0xF0: 'đ', 0xF1: 'ń', 0xF2: 'ň', 0xF5: 'ő', 0xF8: 'ř', 0xF9: 'ů', 0xFB: 'ű', 0xFE: 'ţ', 0xFF: '˙',
	}
	runes := make([]rune, len(data))
	for i, b := range data {
		if r, ok := table[b]; ok {
			runes[i] = r
		} else {
			runes[i] = rune(b)
		}
	}
	return string(runes)
}

func decodeWindows1252(data []byte) string {
	table := map[byte]rune{
		0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…', 0x86: '†', 0x87: '‡', 0x88: 'ˆ', 0x89: '‰', 0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ', 0x8E: 'Ž', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—', 0x98: '˜', 0x99: '™', 0x9A: 'š', 0x9B: '›', 0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ',
	}
	runes := make([]rune, len(data))
	for i, b := range data {
		if r, ok := table[b]; ok {
			runes[i] = r
		} else {
			runes[i] = rune(b)
		}
	}
	return string(runes)
}

func stripHTML(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "<br>", "\n")
	value = strings.ReplaceAll(value, "<br/>", "\n")
	value = strings.ReplaceAll(value, "<br />", "\n")
	var out strings.Builder
	inTag := false
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(out.String())
}

var htmlSpaceRE = regexp.MustCompile(`[ \t\f\v]+`)
var htmlNewlineSpaceRE = regexp.MustCompile(` *\n *`)
var htmlManyNewlinesRE = regexp.MustCompile(`\n{3,}`)
var htmlStyleRE = regexp.MustCompile(`(?is)\bstyle\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
var htmlHrefRE = regexp.MustCompile(`(?is)\bhref\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
var htmlFontWeightRE = regexp.MustCompile(`font-weight\s*:\s*[6-9]00`)

func htmlToRichText(value string) string {
	text, ok := extractHTMLRichText(value)
	if !ok {
		return stripHTML(value)
	}
	return text
}

type htmlMarkers struct {
	bold      bool
	italic    bool
	underline bool
	link      string
}

func extractHTMLRichText(value string) (string, bool) {
	var out strings.Builder
	stack := []htmlMarkers{}
	skipDepth := 0
	for i := 0; i < len(value); {
		if value[i] != '<' {
			j := strings.IndexByte(value[i:], '<')
			if j < 0 {
				j = len(value)
			} else {
				j += i
			}
			if skipDepth == 0 {
				out.WriteString(htmlpkg.UnescapeString(value[i:j]))
			}
			i = j
			continue
		}
		end := strings.IndexByte(value[i:], '>')
		if end < 0 {
			return "", false
		}
		end += i
		tagText := strings.TrimSpace(value[i+1 : end])
		i = end + 1
		if tagText == "" || strings.HasPrefix(tagText, "!") || strings.HasPrefix(tagText, "?") {
			continue
		}
		closing := strings.HasPrefix(tagText, "/")
		if closing {
			tagText = strings.TrimSpace(strings.TrimPrefix(tagText, "/"))
		}
		selfClosing := strings.HasSuffix(tagText, "/")
		if selfClosing {
			tagText = strings.TrimSpace(strings.TrimSuffix(tagText, "/"))
		}
		tag := htmlTagName(tagText)
		if tag == "" {
			continue
		}
		if closing {
			if (tag == "script" || tag == "style") && skipDepth > 0 {
				skipDepth--
				continue
			}
			if skipDepth > 0 {
				continue
			}
			markers := htmlMarkersFor(tag, "")
			if len(stack) > 0 {
				markers = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			}
			writeHTMLMarkerClose(&out, markers)
			if htmlBlockTags[tag] {
				htmlNewline(&out)
			}
			continue
		}
		if tag == "script" || tag == "style" {
			skipDepth++
			continue
		}
		if skipDepth > 0 {
			continue
		}
		if tag == "br" {
			htmlNewline(&out)
			continue
		}
		if htmlBlockTags[tag] {
			htmlNewline(&out)
			if tag == "li" {
				out.WriteString("- ")
			}
		}
		if htmlVoidTags[tag] {
			continue
		}
		markers := htmlMarkersFor(tag, htmlStyle(tagText))
		if tag == "a" {
			markers.link = htmlHref(tagText)
		}
		stack = append(stack, markers)
		writeHTMLMarkerOpen(&out, markers)
		if selfClosing {
			writeHTMLMarkerClose(&out, markers)
			stack = stack[:len(stack)-1]
		}
	}
	text := out.String()
	text = strings.ReplaceAll(text, "\r", "")
	text = htmlSpaceRE.ReplaceAllString(text, " ")
	text = htmlNewlineSpaceRE.ReplaceAllString(text, "\n")
	text = htmlManyNewlinesRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text), true
}

var htmlBlockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "div": true, "dl": true, "fieldset": true, "figcaption": true, "figure": true, "footer": true, "form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true, "hr": true, "li": true, "main": true, "nav": true, "ol": true, "p": true, "pre": true, "section": true, "table": true, "tr": true, "ul": true,
}

var htmlVoidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true,
}

func htmlTagName(tagText string) string {
	fields := strings.Fields(tagText)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func htmlStyle(tagText string) string {
	return htmlAttrValue(tagText, htmlStyleRE)
}

func htmlHref(tagText string) string {
	return htmlpkg.UnescapeString(htmlAttrValue(tagText, htmlHrefRE))
}

func htmlAttrValue(tagText string, re *regexp.Regexp) string {
	match := re.FindStringSubmatch(tagText)
	if len(match) < 2 {
		return ""
	}
	return strings.Trim(match[1], `"'`)
}

func htmlMarkersFor(tag, style string) htmlMarkers {
	compactStyle := strings.ReplaceAll(strings.ToLower(style), " ", "")
	style = strings.ToLower(style)
	return htmlMarkers{
		bold:      tag == "b" || tag == "strong" || strings.Contains(compactStyle, "font-weight:bold") || htmlFontWeightRE.MatchString(style),
		italic:    tag == "i" || tag == "em" || tag == "cite" || strings.Contains(compactStyle, "font-style:italic"),
		underline: tag == "u" || tag == "ins" || strings.Contains(compactStyle, "text-decoration:underline") || strings.Contains(compactStyle, "text-decoration-line:underline"),
	}
}

func htmlNewline(out *strings.Builder) {
	if out.Len() == 0 || strings.HasSuffix(out.String(), "\n") {
		return
	}
	out.WriteByte('\n')
}

func writeHTMLMarkerOpen(out *strings.Builder, markers htmlMarkers) {
	if markers.link != "" {
		out.WriteString("[")
	}
	if markers.bold {
		out.WriteString("**")
	}
	if markers.italic {
		out.WriteString("*")
	}
	if markers.underline {
		out.WriteString("__")
	}
}

func writeHTMLMarkerClose(out *strings.Builder, markers htmlMarkers) {
	if markers.underline {
		out.WriteString("__")
	}
	if markers.italic {
		out.WriteString("*")
	}
	if markers.bold {
		out.WriteString("**")
	}
	if markers.link != "" {
		out.WriteString("](")
		out.WriteString(markers.link)
		out.WriteString(")")
	}
}

func splitAddressHeader(value string) []string {
	value = textutil.CleanHeaderValue(value)
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parser := mail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charsetReader}}
	addrs, err := parser.ParseList(value)
	if err != nil {
		return []string{decodeHeader(value)}
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if display := textutil.CleanHeaderValue(displayAddress(addr)); display != "" {
			out = append(out, display)
		}
	}
	return out
}

func decodeAddressHeader(value string) string {
	items := splitAddressHeader(value)
	if len(items) == 0 {
		return ""
	}
	return textutil.CleanHeaderValue(strings.Join(items, ", "))
}

func decodeAddressHeaders(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := []string{}
	for _, value := range values {
		out = append(out, splitAddressHeader(value)...)
	}
	return out
}

func displayAddress(addr *mail.Address) string {
	if addr == nil {
		return ""
	}
	name := strings.TrimSpace(addr.Name)
	if name == "" {
		return addr.Address
	}
	if strings.ContainsAny(name, `",;<>`) {
		name = strings.ReplaceAll(name, `\`, `\\`)
		name = strings.ReplaceAll(name, `"`, `\"`)
		name = `"` + name + `"`
	}
	return name + " <" + addr.Address + ">"
}

func equalStringSlice(a, b []string) bool {
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

func headerText(raw []byte) string {
	text := string(raw)
	if idx := strings.Index(text, "\r\n\r\n"); idx >= 0 {
		return strings.ReplaceAll(text[:idx], "\r", "")
	}
	if idx := strings.Index(text, "\n\n"); idx >= 0 {
		return strings.ReplaceAll(text[:idx], "\r", "")
	}
	return strings.ReplaceAll(text, "\r", "")
}

func safeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	if name == "" || name == "." || name == string(os.PathSeparator) {
		return "attachment"
	}
	return name
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func messageTime(m *Message) string {
	if m.ReceivedAt != "" {
		return m.ReceivedAt
	}
	return m.SentAt
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
