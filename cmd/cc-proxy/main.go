package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/lobo235/cc-proxy/internal/authcli"
	"github.com/lobo235/cc-proxy/internal/authstore"
	"github.com/lobo235/cc-proxy/internal/config"
	"github.com/lobo235/cc-proxy/internal/modelregistry"
	"github.com/lobo235/cc-proxy/internal/provider"
	codexprovider "github.com/lobo235/cc-proxy/internal/providers/codex"
	codexauth "github.com/lobo235/cc-proxy/internal/providers/codex/auth"
	"github.com/lobo235/cc-proxy/internal/server"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if err != authcli.ErrNotAuthenticated {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	switch first {
	case "--version", "-v", "version":
		fmt.Printf("cc-proxy version %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	case "", "serve":
		return serve()
	case "codex", "kimi":
		return runProviderCommand(first, args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command %q", first)
	}
}

func serve() error {
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	s := server.New(cfg, server.Providers{
		Codex: codexprovider.Provider{
			Client: codexprovider.Client{
				BaseURL:    cfg.Codex.BaseURL,
				AuthStore:  authstore.New(nil, ""),
				Originator: cfg.Codex.Originator,
				UserAgent:  cfg.Codex.UserAgent,
				Version:    version,
			},
			Effort: cfg.Codex.Effort,
		},
		Kimi: provider.NotImplemented{ProviderName: string(modelregistry.ProviderKimi)},
	}, log)
	s.SetVersion(version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("Proxy listening on http://localhost:%d\n", cfg.Port)
	fmt.Printf("Providers are selected per-request by model. %s\n", modelregistry.SupportedMessage(cfg.AliasProvider))
	err = s.ListenAndServe(ctx)
	if err == context.Canceled {
		return nil
	}
	return err
}

func runProviderCommand(name string, args []string) error {
	if len(args) < 2 || args[0] != "auth" {
		usage()
		return fmt.Errorf("invalid %s command", name)
	}
	switch args[1] {
	case "login":
		if name == "codex" {
			fmt.Fprintln(os.Stderr, "codex browser login is not implemented yet; using device login")
			_, err := codexauth.DeviceLogin(context.Background(), codexauth.DeviceLoginOptions{
				Store:  authstore.New(nil, ""),
				Stdout: os.Stdout,
			})
			return err
		}
		return fmt.Errorf("%s auth login not implemented yet", name)
	case "device":
		if name != "codex" {
			return fmt.Errorf("%s auth device not implemented yet", name)
		}
		_, err := codexauth.DeviceLogin(context.Background(), codexauth.DeviceLoginOptions{
			Store:  authstore.New(nil, ""),
			Stdout: os.Stdout,
		})
		return err
	case "status":
		return authcli.Status(name, authcli.Options{Stdout: os.Stdout})
	case "logout":
		return authcli.Logout(name, authcli.Options{Stdout: os.Stdout})
	default:
		usage()
		return fmt.Errorf("invalid %s auth command %q", name, args[1])
	}
}

func usage() {
	fmt.Print(`Usage:
  cc-proxy serve                      Run proxy (PORT env or config.json port, default 18765)
  cc-proxy codex auth login           Browser OAuth (not implemented yet)
  cc-proxy codex auth device          Device-code OAuth (not implemented yet)
  cc-proxy codex auth status          Show current auth
  cc-proxy codex auth logout          Clear stored auth
  cc-proxy kimi auth login            Device-code OAuth (not implemented yet)
  cc-proxy kimi auth status           Show current auth
  cc-proxy kimi auth logout           Clear stored auth
  cc-proxy --version                  Show version
`)
}
