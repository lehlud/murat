package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	cryptobox "lehnert.dev/murat/internal/crypto"
)

type Account struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Email                string `json:"email"`
	Protocol             string `json:"protocol"`
	Username             string `json:"username"`
	Secret               string `json:"secret"`
	AuthKind             string `json:"auth_kind"`
	SessionURL           string `json:"session_url"`
	PrimaryMailAccountID string `json:"primary_mail_account_id,omitempty"`
	IMAPHost             string `json:"imap_host"`
	IMAPPort             int    `json:"imap_port"`
	IMAPMailbox          string `json:"imap_mailbox"`
	SMTPHost             string `json:"smtp_host"`
	SMTPPort             int    `json:"smtp_port"`
	SMTPUsername         string `json:"smtp_username"`
	SMTPSecret           string `json:"smtp_secret"`
	CreatedAt            string `json:"created_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
}

type AccountIndex struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

type AccountStore struct {
	path string
	box  *cryptobox.Box
}

func NewAccountStore(paths Paths, key []byte) (*AccountStore, error) {
	box, err := cryptobox.NewBox(key)
	if err != nil {
		return nil, err
	}
	return &AccountStore{path: paths.AccountsFile, box: box}, nil
}

func (s *AccountStore) All() ([]Account, error) {
	accounts, err := s.load(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return accounts, err
}

func (s *AccountStore) load(path string) ([]Account, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.box.Open(data)
	if err != nil {
		return nil, err
	}
	var index AccountIndex
	if err := json.Unmarshal(plain, &index); err != nil {
		return nil, err
	}
	return index.Accounts, nil
}

func (s *AccountStore) Save(accounts []Account) error {
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Email < accounts[j].Email })
	plain, err := json.Marshal(AccountIndex{Version: 1, Accounts: accounts})
	if err != nil {
		return err
	}
	sealed, err := s.box.Seal(plain)
	if err != nil {
		return err
	}
	return atomicWrite(s.path, sealed, 0o600)
}

func (s *AccountStore) Upsert(account Account) error {
	accounts, err := s.All()
	if err != nil {
		return err
	}
	found := false
	for i := range accounts {
		if accounts[i].ID == account.ID {
			accounts[i] = account
			found = true
			break
		}
	}
	if !found {
		accounts = append(accounts, account)
	}
	return s.Save(accounts)
}

func (s *AccountStore) Remove(id string) error {
	accounts, err := s.All()
	if err != nil {
		return err
	}
	kept := accounts[:0]
	removed := false
	for _, account := range accounts {
		if account.ID == id {
			removed = true
			continue
		}
		kept = append(kept, account)
	}
	if !removed {
		return fmt.Errorf("account not found: %s", id)
	}
	return s.Save(kept)
}

func (s *AccountStore) Get(id string) (Account, error) {
	accounts, err := s.All()
	if err != nil {
		return Account{}, err
	}
	for _, account := range accounts {
		if account.ID == id || strings.EqualFold(account.Email, id) {
			return account, nil
		}
	}
	if id == "" && len(accounts) > 0 {
		return accounts[0], nil
	}
	return Account{}, fmt.Errorf("account not found: %s", id)
}

func StableAccountID(email, endpoint string) string {
	clean := strings.ToLower(strings.TrimSpace(email + "|" + endpoint))
	clean = strings.NewReplacer("@", "-", ":", "-", "/", "-", ".", "-").Replace(clean)
	clean = strings.Trim(clean, "-")
	if clean == "" {
		return "account"
	}
	if len(clean) > 64 {
		return clean[:64]
	}
	return clean
}
