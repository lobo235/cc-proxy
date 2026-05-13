package authcli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lobo235/cc-proxy/internal/authstore"
)

func TestStatusPrintsCodexAuth(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{
		Access:    "access",
		Refresh:   "refresh",
		Expires:   1_765_000_000_000,
		AccountID: "acct_123",
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Status("codex", Options{Stdout: &out, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Account: acct_123",
		"Expires:",
		"Storage:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
}

func TestStatusPrintsKimiAuth(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderKimi, authstore.Record{
		Access:  "access",
		Refresh: "refresh",
		Expires: 1_765_000_000_000,
		UserID:  "user_123",
		Scope:   "openid",
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Status("kimi", Options{Stdout: &out, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"User: user_123",
		"Scope: openid",
		"Storage:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
}

func TestStatusReturnsNotAuthenticated(t *testing.T) {
	var out bytes.Buffer
	err := Status("codex", Options{Stdout: &out, HomeDir: t.TempDir()})
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
	if got := strings.TrimSpace(out.String()); got != "Not authenticated" {
		t.Fatalf("output = %q, want Not authenticated", got)
	}
}

func TestLogoutClearsAuth(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "a", Refresh: "r", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Logout("codex", Options{Stdout: &out, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "Logged out" {
		t.Fatalf("output = %q, want Logged out", got)
	}
	_, _, ok, err := store.Load(authstore.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected auth to be removed")
	}
}
