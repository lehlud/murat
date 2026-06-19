package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMicrosoftRefreshRequestsAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/common/oauth2/v2.0/token" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q", got)
		}
		if got := r.Form.Get("client_id"); got != "client" {
			t.Fatalf("client_id = %q", got)
		}
		if got := r.Form.Get("refresh_token"); got != "old-refresh" {
			t.Fatalf("refresh_token = %q", got)
		}
		scope := r.Form.Get("scope")
		if !strings.Contains(scope, ScopeMicrosoftIMAP) || !strings.Contains(scope, ScopeMicrosoftSMTP) || !strings.Contains(scope, ScopeOfflineAccess) {
			t.Fatalf("scope = %q", scope)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3599}`))
	}))
	defer server.Close()

	token, err := MicrosoftRefresh(context.Background(), MicrosoftConfig{Tenant: "common", ClientID: "client", Endpoint: server.URL}, "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "new-refresh" {
		t.Fatalf("token = %#v", token)
	}
}

func TestMicrosoftPollDeviceCodeWaitsForAuthorization(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/common/oauth2/v2.0/token" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Fatalf("grant_type = %q", got)
		}
		if got := r.Form.Get("device_code"); got != "device" {
			t.Fatalf("device_code = %q", got)
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3599}`))
	}))
	defer server.Close()

	token, err := MicrosoftPollDeviceCode(context.Background(), MicrosoftConfig{Tenant: "common", ClientID: "client", Endpoint: server.URL}, "device", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || calls != 2 {
		t.Fatalf("token = %#v calls = %d", token, calls)
	}
}

func TestMicrosoftDeviceCodeUsesScopes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/organizations/oauth2/v2.0/devicecode" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("client_id"); got != "client" {
			t.Fatalf("client_id = %q", got)
		}
		if got := r.Form.Get("scope"); got != ScopeMicrosoftIMAP+" "+ScopeMicrosoftSMTP+" "+ScopeOfflineAccess {
			t.Fatalf("scope = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_uri":"https://example.com","expires_in":900,"interval":5}`))
	}))
	defer server.Close()

	code, err := MicrosoftDeviceCode(context.Background(), MicrosoftConfig{Tenant: "organizations", ClientID: "client", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if code.DeviceCode != "device" || code.UserCode != "ABCD" {
		t.Fatalf("code = %#v", code)
	}
}
