package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	ScopeMicrosoftIMAP = "https://outlook.office.com/IMAP.AccessAsUser.All"
	ScopeMicrosoftSMTP = "https://outlook.office.com/SMTP.Send"
	ScopeOfflineAccess = "offline_access"
)

type MicrosoftConfig struct {
	Tenant     string
	ClientID   string
	Scopes     []string
	Endpoint   string
	HTTPClient *http.Client
}

type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

type Error struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
	StatusCode  int    `json:"-"`
}

func (e *Error) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("oauth request failed: http %d", e.StatusCode)
}

func DefaultMicrosoftMailScopes() []string {
	return []string{ScopeMicrosoftIMAP, ScopeMicrosoftSMTP, ScopeOfflineAccess}
}

func ParseScopes(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

func ScopeString(scopes []string) string {
	return strings.Join(normalizeScopes(scopes), " ")
}

func MicrosoftDeviceCode(ctx context.Context, cfg MicrosoftConfig) (DeviceCode, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return DeviceCode{}, fmt.Errorf("oauth client id required")
	}
	form := url.Values{
		"client_id": {cfg.ClientID},
		"scope":     {ScopeString(cfg.Scopes)},
	}
	var out DeviceCode
	if err := postForm(ctx, cfg, "devicecode", form, &out); err != nil {
		return DeviceCode{}, err
	}
	if out.DeviceCode == "" {
		return DeviceCode{}, fmt.Errorf("device code response missing device_code")
	}
	return out, nil
}

func MicrosoftPollDeviceCode(ctx context.Context, cfg MicrosoftConfig, deviceCode string, interval time.Duration) (Token, error) {
	if strings.TrimSpace(deviceCode) == "" {
		return Token{}, fmt.Errorf("device code required")
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		token, retry, nextInterval, err := microsoftDeviceToken(ctx, cfg, deviceCode, interval)
		if err == nil {
			return token, nil
		}
		if !retry {
			return Token{}, err
		}
		interval = nextInterval
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Token{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func MicrosoftRefresh(ctx context.Context, cfg MicrosoftConfig, refreshToken string) (Token, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return Token{}, fmt.Errorf("oauth client id required")
	}
	if strings.TrimSpace(refreshToken) == "" {
		return Token{}, fmt.Errorf("oauth refresh token required")
	}
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {ScopeString(cfg.Scopes)},
	}
	var out Token
	if err := postForm(ctx, cfg, "token", form, &out); err != nil {
		return Token{}, err
	}
	if out.AccessToken == "" {
		return Token{}, fmt.Errorf("token response missing access_token")
	}
	return out, nil
}

func microsoftDeviceToken(ctx context.Context, cfg MicrosoftConfig, deviceCode string, interval time.Duration) (Token, bool, time.Duration, error) {
	form := url.Values{
		"client_id":   {cfg.ClientID},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
	}
	var out Token
	err := postForm(ctx, cfg, "token", form, &out)
	if err == nil {
		if out.AccessToken == "" {
			return Token{}, false, interval, fmt.Errorf("token response missing access_token")
		}
		return out, false, interval, nil
	}
	var oauthErr *Error
	if !errors.As(err, &oauthErr) {
		return Token{}, false, interval, err
	}
	switch oauthErr.Code {
	case "authorization_pending":
		return Token{}, true, interval, err
	case "slow_down":
		return Token{}, true, interval + 5*time.Second, err
	default:
		return Token{}, false, interval, err
	}
}

func postForm(ctx context.Context, cfg MicrosoftConfig, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, microsoftURL(cfg, endpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		oauthErr := &Error{StatusCode: res.StatusCode}
		_ = json.Unmarshal(data, oauthErr)
		return oauthErr
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

func microsoftURL(cfg MicrosoftConfig, endpoint string) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if base == "" {
		base = "https://login.microsoftonline.com"
	}
	tenant := strings.TrimSpace(cfg.Tenant)
	if tenant == "" {
		tenant = "common"
	}
	return base + "/" + url.PathEscape(tenant) + "/oauth2/v2.0/" + endpoint
}

func normalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return DefaultMicrosoftMailScopes()
	}
	seen := map[string]bool{}
	out := []string{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	if len(out) == 0 {
		return DefaultMicrosoftMailScopes()
	}
	return out
}
