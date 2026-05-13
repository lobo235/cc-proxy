package authcli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lobo235/cc-proxy/internal/authstore"
)

var ErrNotAuthenticated = errors.New("not authenticated")

type Options struct {
	Stdout  io.Writer
	Env     map[string]string
	HomeDir string
}

func Status(providerName string, opts Options) error {
	provider, err := authstore.ParseProvider(providerName)
	if err != nil {
		return err
	}
	out := opts.Stdout
	if out == nil {
		out = io.Discard
	}
	store := authstore.New(opts.Env, opts.HomeDir)
	rec, path, ok, err := store.Load(provider)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "Not authenticated")
		return ErrNotAuthenticated
	}
	printStatus(out, provider, rec, path, time.Now())
	return nil
}

func Logout(providerName string, opts Options) error {
	provider, err := authstore.ParseProvider(providerName)
	if err != nil {
		return err
	}
	if err := authstore.New(opts.Env, opts.HomeDir).Clear(provider); err != nil {
		return err
	}
	out := opts.Stdout
	if out == nil {
		out = io.Discard
	}
	fmt.Fprintln(out, "Logged out")
	return nil
}

func printStatus(out io.Writer, provider authstore.Provider, rec authstore.Record, path string, now time.Time) {
	// Keep provider-specific labels compatible with the upstream CLI.
	switch provider {
	case authstore.ProviderCodex:
		fmt.Fprintf(out, "Account: %s\n", fallback(rec.AccountID, "(none)"))
	case authstore.ProviderKimi:
		fmt.Fprintf(out, "User: %s\n", fallback(rec.UserID, "(none)"))
	default:
		fmt.Fprintf(out, "Provider: %s\n", provider)
	}
	expires := time.UnixMilli(rec.Expires).UTC().Format(time.RFC3339Nano)
	fmt.Fprintf(out, "Expires: %s (in %ds)\n", expires, int(time.UnixMilli(rec.Expires).Sub(now).Seconds()))
	if provider == authstore.ProviderKimi {
		fmt.Fprintf(out, "Scope: %s\n", fallback(rec.Scope, "(none)"))
	}
	fmt.Fprintf(out, "Storage: %s\n", path)
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
}
