package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const DefaultPort = 18765

type AliasProvider string

const (
	AliasProviderCodex AliasProvider = "codex"
	AliasProviderKimi  AliasProvider = "kimi"
)

type FileConfig struct {
	Port          *int          `json:"port,omitempty"`
	AliasProvider AliasProvider `json:"aliasProvider,omitempty"`
	Codex         CodexConfig   `json:"codex,omitempty"`
	Kimi          KimiConfig    `json:"kimi,omitempty"`
	Log           LogConfig     `json:"log,omitempty"`
}

type CodexConfig struct {
	Originator     string `json:"originator,omitempty"`
	UserAgent      string `json:"userAgent,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	ServiceTier    string `json:"serviceTier,omitempty"`
	BaseURL        string `json:"baseUrl,omitempty"`
	InputTokensURL string `json:"inputTokensUrl,omitempty"`
}

type KimiConfig struct {
	UserAgent string `json:"userAgent,omitempty"`
	OAuthHost string `json:"oauthHost,omitempty"`
	BaseURL   string `json:"baseUrl,omitempty"`
}

type LogConfig struct {
	Stderr  bool `json:"stderr,omitempty"`
	Verbose bool `json:"verbose,omitempty"`
}

type Config struct {
	Port          int
	AliasProvider AliasProvider
	Codex         CodexConfig
	Kimi          KimiConfig
	Log           LogConfig
	ConfigPath    string
}

type LoadOptions struct {
	Env        map[string]string
	ConfigPath string
	Stderr     io.Writer
	HomeDir    string
}

func Load(opts LoadOptions) (Config, error) {
	env := opts.Env
	if env == nil {
		env = environ()
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	path := opts.ConfigPath
	if path == "" {
		path = ConfigFilePath(env, opts.HomeDir)
	}

	file := readFileConfig(path, stderr)

	cfg := Config{
		Port:          DefaultPort,
		AliasProvider: AliasProviderCodex,
		ConfigPath:    path,
	}
	if file.Port != nil {
		cfg.Port = *file.Port
	}
	if file.AliasProvider == AliasProviderCodex || file.AliasProvider == AliasProviderKimi {
		cfg.AliasProvider = file.AliasProvider
	}
	cfg.Codex = file.Codex
	cfg.Kimi = file.Kimi
	cfg.Log = file.Log

	if v := env["PORT"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid PORT %q", v)
		}
		cfg.Port = n
	}
	if v := env["CCP_ALIAS_PROVIDER"]; v != "" {
		p, err := ParseAliasProvider(v)
		if err != nil {
			return Config{}, err
		}
		cfg.AliasProvider = p
	}

	cfg.Codex.Originator = firstSet(env["CCP_CODEX_ORIGINATOR"], env["CCP_ORIGINATOR"], cfg.Codex.Originator)
	cfg.Codex.UserAgent = firstSet(env["CCP_CODEX_USER_AGENT"], env["CCP_USER_AGENT"], cfg.Codex.UserAgent)
	cfg.Codex.Model = firstNonEmpty(env["CCP_CODEX_MODEL"], cfg.Codex.Model)
	cfg.Codex.Effort = firstNonEmpty(env["CCP_CODEX_EFFORT"], cfg.Codex.Effort)
	cfg.Codex.ServiceTier = firstNonEmpty(env["CCP_CODEX_SERVICE_TIER"], cfg.Codex.ServiceTier)
	cfg.Codex.BaseURL = firstSet(env["CCP_CODEX_BASE_URL"], cfg.Codex.BaseURL)
	cfg.Codex.InputTokensURL = firstSet(env["CCP_CODEX_INPUT_TOKENS_URL"], cfg.Codex.InputTokensURL)

	cfg.Kimi.UserAgent = firstSet(env["CCP_KIMI_USER_AGENT"], env["CCP_USER_AGENT"], cfg.Kimi.UserAgent)
	cfg.Kimi.OAuthHost = firstSet(env["CCP_KIMI_OAUTH_HOST"], cfg.Kimi.OAuthHost)
	cfg.Kimi.BaseURL = firstSet(env["CCP_KIMI_BASE_URL"], cfg.Kimi.BaseURL)

	if _, ok := env["CCP_LOG_STDERR"]; ok {
		cfg.Log.Stderr = true
	}
	if _, ok := env["CCP_LOG_VERBOSE"]; ok {
		cfg.Log.Verbose = true
	}

	return cfg, nil
}

func ParseAliasProvider(v string) (AliasProvider, error) {
	switch AliasProvider(v) {
	case AliasProviderCodex:
		return AliasProviderCodex, nil
	case AliasProviderKimi:
		return AliasProviderKimi, nil
	default:
		return "", fmt.Errorf("invalid alias provider %q: expected codex or kimi", v)
	}
}

func ConfigDir(env map[string]string, home string) string {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if xdg := env["XDG_CONFIG_HOME"]; xdg != "" {
		return filepath.Join(xdg, "claude-code-proxy")
	}
	return filepath.Join(home, ".config", "claude-code-proxy")
}

func StateDir(env map[string]string, home string) string {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if xdg := env["XDG_STATE_HOME"]; xdg != "" {
		return filepath.Join(xdg, "claude-code-proxy")
	}
	return filepath.Join(home, ".local", "state", "claude-code-proxy")
}

func ConfigFilePath(env map[string]string, home string) string {
	return filepath.Join(ConfigDir(env, home), "config.json")
}

func readFileConfig(path string, stderr io.Writer) FileConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "cc-proxy: failed to read %s (%v); using defaults\n", path, err)
		}
		return FileConfig{}
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(stderr, "cc-proxy: failed to parse %s (%v); using defaults\n", path, err)
		return FileConfig{}
	}
	return cfg
}

func environ() map[string]string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return env
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	return firstSet(values...)
}
