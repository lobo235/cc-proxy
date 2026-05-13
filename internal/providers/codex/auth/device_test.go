package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lobo235/cc-proxy/internal/authstore"
)

func TestDeviceLoginPollsAndStoresCodexAuth(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	polls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			fmt.Fprint(w, `{"device_auth_id":"dev_123","user_code":"ABCD-EFGH","interval":"1"}`)
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `{"authorization_code":"auth_code","code_verifier":"verifier"}`)
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "auth_code" {
				t.Fatalf("token form = %v", r.Form)
			}
			fmt.Fprint(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	var out strings.Builder
	rec, err := DeviceLogin(context.Background(), DeviceLoginOptions{
		Issuer:       upstream.URL,
		Store:        store,
		Stdout:       &out,
		PollInterval: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Access != "access" || rec.Refresh != "refresh" || rec.Expires <= 0 {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(out.String(), "Visit: "+upstream.URL+"/codex/device") || !strings.Contains(out.String(), "Enter code: ABCD-EFGH") {
		t.Fatalf("output = %s", out.String())
	}
	loaded, _, ok, err := store.Load(authstore.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || loaded.Access != "access" {
		t.Fatalf("loaded = %+v ok=%v", loaded, ok)
	}
}

func TestExtractAccountIDFromJWTClaims(t *testing.T) {
	token := "header.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGguY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdF8xMjMifQ.signature"
	if got := ExtractAccountID(token); got != "acct_123" {
		t.Fatalf("account id = %q, want acct_123", got)
	}
}
