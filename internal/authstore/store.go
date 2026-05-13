package authstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lobo235/cc-proxy/internal/config"
)

type Provider string

const (
	ProviderCodex Provider = "codex"
	ProviderKimi  Provider = "kimi"
)

type Record struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId,omitempty"`
	Scope     string `json:"scope,omitempty"`
	UserID    string `json:"userId,omitempty"`
}

type Store struct {
	env  map[string]string
	home string
}

func New(env map[string]string, home string) Store {
	if env == nil {
		env = map[string]string{}
	}
	return Store{env: env, home: home}
}

func ParseProvider(name string) (Provider, error) {
	switch Provider(name) {
	case ProviderCodex:
		return ProviderCodex, nil
	case ProviderKimi:
		return ProviderKimi, nil
	default:
		return "", fmt.Errorf("unknown auth provider %q", name)
	}
}

func (s Store) AuthPath(provider Provider) string {
	return filepath.Join(config.ConfigDir(s.env, s.home), string(provider), "auth.json")
}

func (s Store) LegacyAuthPath(provider Provider) string {
	home := s.home
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, ".config", "claude-code-proxy", string(provider), "auth.json")
}

func (s Store) Load(provider Provider) (Record, string, bool, error) {
	primary := s.AuthPath(provider)
	rec, ok, err := readRecord(primary)
	if err != nil {
		return Record{}, primary, false, err
	}
	if ok {
		return rec, primary, true, nil
	}

	legacy := s.LegacyAuthPath(provider)
	if legacy == primary {
		return Record{}, primary, false, nil
	}
	rec, ok, err = readRecord(legacy)
	if err != nil {
		return Record{}, legacy, false, err
	}
	if ok {
		return rec, legacy, true, nil
	}
	return Record{}, primary, false, nil
}

func (s Store) Save(provider Provider, rec Record) error {
	path := s.AuthPath(provider)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating auth dir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding auth record: %w", err)
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing auth temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("installing auth file: %w", err)
	}
	return nil
}

func (s Store) Clear(provider Provider) error {
	var firstErr error
	for _, path := range []string{s.AuthPath(provider), s.LegacyAuthPath(provider)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return fmt.Errorf("clearing auth: %w", firstErr)
	}
	return nil
}

func readRecord(path string) (Record, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, false, nil
		}
		return Record{}, false, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	return rec, true, nil
}
