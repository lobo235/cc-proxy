package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(LoadOptions{
		Env:        map[string]string{},
		ConfigPath: filepath.Join(t.TempDir(), "missing.json"),
		Stderr:     &bytes.Buffer{},
		HomeDir:    "/home/tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != DefaultPort {
		t.Fatalf("port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.AliasProvider != AliasProviderCodex {
		t.Fatalf("alias provider = %q", cfg.AliasProvider)
	}
}

func TestLoadEnvOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"port": 1111, "aliasProvider": "kimi", "codex": {"model": "gpt-5.4"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{
		Env: map[string]string{
			"PORT":                        "2222",
			"CCP_ALIAS_PROVIDER":          "codex",
			"CCP_CODEX_MODEL":             "gpt-5.5",
			"CCP_CODEX_COMPACTION_EFFORT": "low",
			"CCP_CODEX_INPUT_TOKENS_URL":  "https://example.test/input_tokens",
		},
		ConfigPath: path,
		Stderr:     &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 2222 {
		t.Fatalf("port = %d, want 2222", cfg.Port)
	}
	if cfg.AliasProvider != AliasProviderCodex {
		t.Fatalf("alias provider = %q, want codex", cfg.AliasProvider)
	}
	if cfg.Codex.Model != "gpt-5.5" {
		t.Fatalf("codex model = %q, want gpt-5.5", cfg.Codex.Model)
	}
	if cfg.Codex.CompactionEffort != "low" {
		t.Fatalf("codex compaction effort = %q, want low", cfg.Codex.CompactionEffort)
	}
	if cfg.Codex.InputTokensURL != "https://example.test/input_tokens" {
		t.Fatalf("codex input token url = %q", cfg.Codex.InputTokensURL)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	_, err := Load(LoadOptions{
		Env:        map[string]string{"PORT": "nope"},
		ConfigPath: filepath.Join(t.TempDir(), "missing.json"),
		Stderr:     &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected invalid port error")
	}
}
