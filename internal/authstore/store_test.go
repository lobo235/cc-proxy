package authstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveLoadAndClear(t *testing.T) {
	home := t.TempDir()
	store := New(map[string]string{}, home)
	rec := Record{
		Access:    "access",
		Refresh:   "refresh",
		Expires:   1_765_000_000_000,
		AccountID: "acct",
	}
	if err := store.Save(ProviderCodex, rec); err != nil {
		t.Fatal(err)
	}
	loaded, path, ok, err := store.Load(ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected auth record")
	}
	if path != store.AuthPath(ProviderCodex) {
		t.Fatalf("path = %q, want %q", path, store.AuthPath(ProviderCodex))
	}
	if loaded.AccountID != rec.AccountID || loaded.Expires != rec.Expires {
		t.Fatalf("loaded = %+v, want %+v", loaded, rec)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %v, want 0600", got)
	}
	if err := store.Clear(ProviderCodex); err != nil {
		t.Fatal(err)
	}
	_, _, ok, err = store.Load(ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected auth to be cleared")
	}
}

func TestLoadFallsBackToLegacyPath(t *testing.T) {
	home := t.TempDir()
	store := New(map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, "xdg")}, home)
	legacyPath := store.LegacyAuthPath(ProviderKimi)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"access":"a","refresh":"r","expires":1765000000000,"userId":"u"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, path, ok, err := store.Load(ProviderKimi)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected legacy auth record")
	}
	if path != legacyPath {
		t.Fatalf("path = %q, want %q", path, legacyPath)
	}
	if rec.UserID != "u" {
		t.Fatalf("user id = %q, want u", rec.UserID)
	}
}
