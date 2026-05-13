package modelregistry

import (
	"strings"
	"testing"

	"github.com/lobo235/cc-proxy/internal/config"
)

func TestNormalizeIncomingModel(t *testing.T) {
	got := NormalizeIncomingModel("gpt-5.4[1m]")
	if got != "gpt-5.4" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveCodexFastAlias(t *testing.T) {
	got, ok := Resolve("gpt-5.4-fast", config.AliasProviderCodex)
	if !ok {
		t.Fatal("expected resolve")
	}
	if got.Provider != ProviderCodex || got.Model != "gpt-5.4" || got.ServiceTier != "priority" {
		t.Fatalf("unexpected resolve: %+v", got)
	}
}

func TestResolveAnthropicAliasViaKimi(t *testing.T) {
	got, ok := Resolve("claude-sonnet-4-6", config.AliasProviderKimi)
	if !ok {
		t.Fatal("expected resolve")
	}
	if got.Provider != ProviderKimi || got.Model != "kimi-for-coding" {
		t.Fatalf("unexpected resolve: %+v", got)
	}
}

func TestSupportedMessageIncludesProviders(t *testing.T) {
	msg := SupportedMessage(config.AliasProviderCodex)
	for _, want := range []string{"codex:", "kimi:", "gpt-5.5", "kimi-for-coding"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("supported message %q missing %q", msg, want)
		}
	}
}
